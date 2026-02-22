package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/toolhub/toolhub/internal/telemetry"
)

type Client struct {
	appID          int64
	installationID int64
	privateKey     *rsa.PrivateKey
	httpClient     *http.Client

	mu    sync.Mutex
	token string
	expAt time.Time
}

func NewClient(appID, installationID int64, keyPath string) (*Client, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", keyPath)
	}

	key, err := parseRSAPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return &Client{
		appID:          appID,
		installationID: installationID,
		privateKey:     key,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func parseRSAPrivateKey(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}

	pkcs8Key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := pkcs8Key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return rsaKey, nil
}

// SECURITY: JWT signed with RS256 per GitHub App spec.
// 10 min expiry; refreshed with 1 min safety buffer.
func (c *Client) makeJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(c.appID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}

type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type installationInfo struct {
	ID int64 `json:"id"`
}

func (c *Client) ensureInstallationID(ctx context.Context) error {
	if c.installationID != 0 {
		return nil
	}

	jwtStr, err := c.makeJWT()
	if err != nil {
		return fmt.Errorf("sign JWT: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/app/installations?per_page=100", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discover installation id: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discover installation id HTTP %d: %s", resp.StatusCode, body)
	}

	var installations []installationInfo
	if err := json.NewDecoder(resp.Body).Decode(&installations); err != nil {
		return fmt.Errorf("decode installations response: %w", err)
	}

	if len(installations) == 0 {
		return fmt.Errorf("no installation found for this GitHub App")
	}
	if len(installations) > 1 {
		return fmt.Errorf("multiple installations found (%d), set GITHUB_INSTALLATION_ID explicitly", len(installations))
	}

	c.installationID = installations[0].ID
	return nil
}

func (c *Client) installationToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureInstallationID(ctx); err != nil {
		return "", err
	}

	if c.token != "" && time.Now().Before(c.expAt.Add(-time.Minute)) {
		return c.token, nil
	}

	jwtStr, err := c.makeJWT()
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", c.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("installation token HTTP %d: %s", resp.StatusCode, body)
	}

	var tok installationTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	c.token = tok.Token
	c.expAt = tok.ExpiresAt
	return c.token, nil
}

func (c *Client) doAPI(ctx context.Context, method, url string, body any) (*http.Response, error) {
	token, err := c.installationToken(ctx)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

type CreateIssueInput struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

type Issue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
}

type Comment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

type PullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Draft     bool   `json:"draft"`
	HTMLURL   string `json:"html_url"`
	Merged    bool   `json:"merged"`
	Mergeable *bool  `json:"mergeable,omitempty"`
	Base      struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

type CreatePullRequestInput struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body,omitempty"`
}

type PullRequestFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	BlobURL          string `json:"blob_url"`
	RawURL           string `json:"raw_url"`
	Patch            string `json:"patch,omitempty"`
	PreviousFilename string `json:"previous_filename,omitempty"`
}

type APIError struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s HTTP %d: %s", e.Operation, e.StatusCode, e.Body)
}

func (c *Client) CreateIssue(ctx context.Context, owner, repo string, in CreateIssueInput) (*Issue, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues", owner, repo)
	const maxAttempts = 4

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := c.doAPI(ctx, http.MethodPost, url, in)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts && isRetryableError(err) {
				if !sleepWithBackoff(ctx, attempt, 0) {
					return nil, ctx.Err()
				}
				continue
			}
			return nil, err
		}

		if resp.StatusCode == http.StatusCreated {
			defer resp.Body.Close()
			var issue Issue
			if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
				return nil, fmt.Errorf("decode issue: %w", err)
			}
			return &issue, nil
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("create issue HTTP %d and read body failed: %w", resp.StatusCode, readErr)
		} else {
			telemetry.IncGitHubAPIError("create issue", resp.StatusCode)
			lastErr = &APIError{Operation: "create issue", StatusCode: resp.StatusCode, Body: string(body)}
		}

		retryAfter := retryAfterDuration(resp)
		if attempt < maxAttempts && isRetryableStatus(resp.StatusCode) {
			if !sleepWithBackoff(ctx, attempt, retryAfter) {
				return nil, ctx.Err()
			}
			continue
		}

		return nil, lastErr
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("create issue failed")
	}
	return nil, lastErr
}

func (c *Client) CreatePRComment(ctx context.Context, owner, repo string, prNumber int, bodyText string) (*Comment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, prNumber)
	payload := map[string]string{"body": bodyText}

	resp, err := c.doAPI(ctx, http.MethodPost, url, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("create pr comment HTTP %d and read body failed: %w", resp.StatusCode, readErr)
		}
		telemetry.IncGitHubAPIError("create pr comment", resp.StatusCode)
		return nil, &APIError{Operation: "create pr comment", StatusCode: resp.StatusCode, Body: string(b)}
	}

	var comment Comment
	if err := json.NewDecoder(resp.Body).Decode(&comment); err != nil {
		return nil, fmt.Errorf("decode pr comment: %w", err)
	}
	return &comment, nil
}

func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*PullRequest, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, prNumber)
	resp, err := c.doAPI(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("get pull request HTTP %d and read body failed: %w", resp.StatusCode, readErr)
		}
		telemetry.IncGitHubAPIError("get pull request", resp.StatusCode)
		return nil, &APIError{Operation: "get pull request", StatusCode: resp.StatusCode, Body: string(body)}
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode pull request: %w", err)
	}
	return &pr, nil
}

func (c *Client) ListPullRequestFiles(ctx context.Context, owner, repo string, prNumber int) ([]PullRequestFile, error) {
	files := make([]PullRequestFile, 0)
	for page := 1; page <= 10; page++ {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/files?per_page=100&page=%d", owner, repo, prNumber, page)
		resp, err := c.doAPI(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("list pull request files HTTP %d and read body failed: %w", resp.StatusCode, readErr)
			}
			telemetry.IncGitHubAPIError("list pull request files", resp.StatusCode)
			return nil, &APIError{Operation: "list pull request files", StatusCode: resp.StatusCode, Body: string(body)}
		}

		var pageFiles []PullRequestFile
		if err := json.NewDecoder(resp.Body).Decode(&pageFiles); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode pull request files: %w", err)
		}
		resp.Body.Close()

		files = append(files, pageFiles...)
		if len(pageFiles) < 100 {
			break
		}
	}
	return files, nil
}

func (c *Client) CreatePullRequest(ctx context.Context, owner, repo string, in CreatePullRequestInput) (*PullRequest, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)
	resp, err := c.doAPI(ctx, http.MethodPost, url, in)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("create pull request HTTP %d and read body failed: %w", resp.StatusCode, readErr)
		}
		telemetry.IncGitHubAPIError("create pull request", resp.StatusCode)
		return nil, &APIError{Operation: "create pull request", StatusCode: resp.StatusCode, Body: string(body)}
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode pull request: %w", err)
	}
	return &pr, nil
}

func (c *Client) BatchCreateIssues(ctx context.Context, owner, repo string, issues []CreateIssueInput) ([]BatchResult, error) {
	results := make([]BatchResult, len(issues))
	for i, in := range issues {
		issue, err := c.CreateIssue(ctx, owner, repo, in)
		results[i] = BatchResult{Index: i, Issue: issue, Err: err}
	}
	return results, nil
}

func isRetryableStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500 && code <= 599
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func retryAfterDuration(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

func sleepWithBackoff(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	base := 250 * time.Millisecond
	max := 5 * time.Second
	backoff := base * time.Duration(1<<(attempt-1))
	if backoff > max {
		backoff = max
	}
	jitter := time.Duration(rand.Intn(200)) * time.Millisecond
	wait := backoff + jitter
	if retryAfter > wait {
		wait = retryAfter
	}

	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type BatchResult struct {
	Index int    `json:"index"`
	Issue *Issue `json:"issue,omitempty"`
	Err   error  `json:"error,omitempty"`
}
