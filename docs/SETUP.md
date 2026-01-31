# Repository Setup Guide

This document explains how to set up the necessary infrastructure for automated releases to GitHub, Homebrew, and AUR.

## Prerequisites

### 1. Homebrew Tap Repository

Create a Homebrew tap repository at `https://github.com/aleksclark/homebrew-tap`:

```bash
# Create the repository on GitHub first, then:
git clone https://github.com/aleksclark/homebrew-tap
cd homebrew-tap
mkdir -p Formula
echo "# Homebrew Tap for Crush Modules" > README.md
git add .
git commit -m "Initial commit"
git push origin main
```

### 2. AUR Repositories

Initialize AUR package repositories (requires AUR account):

```bash
# Generate SSH key for AUR if you don't have one
ssh-keygen -t ed25519 -C "aleks.clark@gmail.com" -f ~/.ssh/aur

# Add the public key to your AUR account:
# https://aur.archlinux.org/account/

# Initialize the package repositories
git clone ssh://aur@aur.archlinux.org/crush-extended-bin.git
cd crush-extended-bin
# Initial PKGBUILD will be created by goreleaser
git commit --allow-empty -m "Initial commit"
git push origin master

git clone ssh://aur@aur.archlinux.org/xcrush-bin.git
cd xcrush-bin
# Initial PKGBUILD will be created by goreleaser
git commit --allow-empty -m "Initial commit"
git push origin master
```

## GitHub Secrets

Configure the following secrets in your GitHub repository settings (`Settings` → `Secrets and variables` → `Actions`):

### Required Secrets

1. **`HOMEBREW_TAP_GITHUB_TOKEN`**
   - **Purpose**: Allows goreleaser to push formula updates to homebrew-tap
   - **How to create**:
     1. Go to GitHub Settings → Developer settings → Personal access tokens → Tokens (classic)
     2. Click "Generate new token (classic)"
     3. Name: "GoReleaser Homebrew"
     4. Select scopes: `repo` (all), `workflow`
     5. Click "Generate token"
     6. Copy the token (you won't see it again)
   - **Add to repository**:
     1. Go to repository Settings → Secrets and variables → Actions
     2. Click "New repository secret"
     3. Name: `HOMEBREW_TAP_GITHUB_TOKEN`
     4. Value: Paste the token
     5. Click "Add secret"

2. **`AUR_SSH_KEY`**
   - **Purpose**: Allows goreleaser to push package updates to AUR
   - **How to create**:
     ```bash
     # Display your private key (the one registered with AUR)
     cat ~/.ssh/aur
     ```
   - **Add to repository**:
     1. Go to repository Settings → Secrets and variables → Actions
     2. Click "New repository secret"
     3. Name: `AUR_SSH_KEY`
     4. Value: Paste the ENTIRE private key (including `-----BEGIN` and `-----END` lines)
     5. Click "Add secret"

### Verification

The secrets should look like:

- ✅ `GITHUB_TOKEN` (automatically provided by GitHub Actions)
- ✅ `HOMEBREW_TAP_GITHUB_TOKEN` (you added this)
- ✅ `AUR_SSH_KEY` (you added this)

## How Releases Work

### Automatic Release on Push to Master

Every push to the `master` branch triggers an automatic release:

1. **CalVer tag generation**: Creates tag like `2026.01.30.1` (today's date + build number)
2. **Pre-build**: Runs `scripts/prebuild.sh` to build crush-extended binaries
3. **GoReleaser**: Builds xcrush, packages everything, creates release
4. **Distribution**:
   - GitHub Releases with .tar.gz, .deb, .rpm packages
   - Homebrew formulas pushed to `homebrew-tap` repository
   - AUR PKGBUILD files pushed to AUR repositories

### What Gets Published

| Package | GitHub | Homebrew | AUR |
|---------|--------|----------|-----|
| crush-extended | ✅ | ✅ | ✅ `crush-extended-bin` |
| xcrush | ✅ | ✅ | ✅ `xcrush-bin` |

### Timeline

- **Commit pushed to master** → **~5-10 minutes** → **Packages available**

## Installation After Release

### Homebrew
```bash
brew tap aleksclark/tap
brew install crush-extended
# or
brew install xcrush
```

### AUR (Arch Linux)
```bash
# Using yay
yay -S crush-extended-bin

# Using paru
paru -S crush-extended-bin

# Manual
git clone https://aur.archlinux.org/crush-extended-bin.git
cd crush-extended-bin
makepkg -si
```

### Debian/Ubuntu
```bash
# Download from GitHub Releases
curl -LO https://github.com/aleksclark/crush-modules/releases/latest/download/crush-extended_VERSION_linux_x86_64.deb
sudo dpkg -i crush-extended_VERSION_linux_x86_64.deb
```

## Troubleshooting

### Homebrew Formula Not Updating

1. Check GitHub Actions logs for errors
2. Verify `HOMEBREW_TAP_GITHUB_TOKEN` has correct permissions
3. Check that homebrew-tap repository exists and is public

### AUR Package Not Updating

1. Check GitHub Actions logs for AUR-related errors
2. Verify `AUR_SSH_KEY` is the correct private key
3. Test SSH access manually:
   ```bash
   ssh -i ~/.ssh/aur aur@aur.archlinux.org
   # Should show: "Hi username, You've successfully authenticated..."
   ```
4. Ensure AUR repositories were initialized with empty commits

### Release Failed

1. Check GitHub Actions workflow logs
2. Common issues:
   - Missing crush-plugin-poc checkout
   - Go version mismatch
   - Permission issues with secrets
   - Network timeouts during package uploads

## Testing Locally

Before pushing to master, test the release process locally:

```bash
# Build binaries
./scripts/prebuild.sh

# Test goreleaser (requires goreleaser installed)
goreleaser release --snapshot --clean --skip=publish

# Check generated files in dist/
ls -la dist/
```

## Disabling Auto-Release

To disable automatic releases temporarily:

1. Edit `.github/workflows/release.yaml`
2. Comment out the `on: push: branches: - master` section
3. Commit and push

Or disable the workflow in GitHub:
1. Go to Actions tab
2. Select "Release" workflow
3. Click "..." → "Disable workflow"
