# Secrets

This folder is **not** committed to git.

## GitHub App private key

Place your GitHub App private key here:

- `./secrets/github_app_private_key.pem`

The docker-compose setup mounts it read-only into the ToolHub container at:

- `/run/secrets/github_app_private_key.pem`

**Do not** commit the `.pem` file.
