# Generating a GitHub Token for Archie Core

This guide walks you through creating a GitHub Personal Access Token (PAT) for Archie Core.

> **Important**: Archie Core requires a **Classic Personal Access Token** (starts with `ghp_...`). Fine-grained PATs currently lack necessary scope permissions for Git HTTP push operations, repository invitation acceptance, and cross-repository operations needed by the orchestrator.

---

## Why Archie Core Needs a GitHub Token
Archie Core uses the GitHub API to:
1. Poll assigned or labelled issues (`archie:queued`).
2. Update issue labels as tasks transition (`archie:working`, `archie:pr`, `archie:parked`).
3. Post status comments and emoji reactions.
4. Create branches, commit code changes, and open Pull Requests for human review.

---

## Step-by-Step Instructions: Creating a Classic Token

1. Navigate to [GitHub Settings → Developer Settings → Tokens (classic)](https://github.com/settings/tokens).
2. Click **Generate new token** → **Generate new token (classic)**.
3. Set the **Note**: `Archie Core` (or your bot's name).
4. Choose an **Expiration** (e.g. 90 days, 1 year, or No expiration for dedicated bot accounts).
5. Select the required scope:
   - **`repo`** (Full control of private repositories — required for polling, commenting, pushing branches, and opening PRs).
   - **`workflow`** (Optional — required if Archie needs to update GitHub Actions workflows).
6. Scroll to the bottom and click **Generate token**.
7. **Copy your token immediately** (starts with `ghp_...`). You won't be able to see it again!

---

## Configuring the Token in Archie Core

Save your token using any of the following methods:

### Method A: Environment File (Default XDG Location)
Add your token to `${XDG_CONFIG_HOME:-~/.config}/archie/env`:
```bash
ARCHIE_GITHUB_TOKEN="ghp_YOUR_CLASSIC_TOKEN_HERE"
```

### Method B: Environment Variable in Current Shell
```bash
export ARCHIE_GITHUB_TOKEN="ghp_YOUR_CLASSIC_TOKEN_HERE"
```

### Method C: Directly in Configuration File (`~/.config/archie/config.toml`)
```toml
[forge]
type = "github"
host = "https://github.com"
token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }
```
