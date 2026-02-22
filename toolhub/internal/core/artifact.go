package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/toolhub/toolhub/internal/db"
)

// ArtifactStore persists opaque payloads to the local filesystem and records
// metadata in PostgreSQL. SHA-256 is computed on write for tamper evidence.
type ArtifactStore struct {
	db      *db.DB
	baseDir string
}

// NewArtifactStore returns a store rooted at baseDir.
func NewArtifactStore(database *db.DB, baseDir string) (*ArtifactStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifact dir: %w", err)
	}
	return &ArtifactStore{db: database, baseDir: baseDir}, nil
}

// SaveInput holds parameters for saving a new artifact.
type SaveInput struct {
	RunID       string
	Name        string
	ContentType string
	Body        io.Reader
}

// Save writes body to disk, computes its SHA-256, and inserts a DB record.
func (s *ArtifactStore) Save(ctx context.Context, in SaveInput) (*db.Artifact, error) {
	id := uuid.New().String()
	dir := filepath.Join(s.baseDir, in.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir artifact: %w", err)
	}

	fpath := filepath.Join(dir, id)
	f, err := os.Create(fpath)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}

	h := sha256.New()
	w := io.MultiWriter(f, h)

	n, err := io.Copy(w, in.Body)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(fpath)
		return nil, fmt.Errorf("write artifact: %w", err)
	}

	art := &db.Artifact{
		ArtifactID:  id,
		RunID:       in.RunID,
		Name:        in.Name,
		URI:         "file://" + fpath,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
		SizeBytes:   n,
		ContentType: in.ContentType,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.db.InsertArtifact(ctx, art); err != nil {
		os.Remove(fpath)
		return nil, err
	}
	return art, nil
}

// Get retrieves artifact metadata by ID.
func (s *ArtifactStore) Get(ctx context.Context, artifactID string) (*db.Artifact, error) {
	return s.db.GetArtifact(ctx, artifactID)
}

// GetByRunAndID retrieves artifact metadata by run ID and artifact ID.
func (s *ArtifactStore) GetByRunAndID(ctx context.Context, runID, artifactID string) (*db.Artifact, error) {
	return s.db.GetArtifactByRunAndID(ctx, runID, artifactID)
}

// ListByRun returns all artifacts belonging to a run.
func (s *ArtifactStore) ListByRun(ctx context.Context, runID string) ([]*db.Artifact, error) {
	return s.db.ListArtifactsByRun(ctx, runID)
}

func (s *ArtifactStore) Read(ctx context.Context, artifactID string) ([]byte, error) {
	art, err := s.Get(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if art == nil {
		return nil, fmt.Errorf("artifact not found: %s", artifactID)
	}
	if !strings.HasPrefix(art.URI, "file://") {
		return nil, fmt.Errorf("unsupported artifact URI: %s", art.URI)
	}
	path := strings.TrimPrefix(art.URI, "file://")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read artifact file: %w", err)
	}
	return b, nil
}
