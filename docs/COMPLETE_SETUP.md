# Complete Setup Guide - Automated

This guide walks you through setting up Homebrew and AUR repositories with automated scripts.

## Quick Start (Automated)

Run the automated setup script:

```bash
cd /home/aleks/work/personal/crush_dev/crush-modules
./scripts/setup-repositories.sh
```

This script will:
1. Create and push the `aleksclark/homebrew-tap` GitHub repository
2. Set up AUR SSH access (creates key if needed)
3. Initialize AUR packages (`crush-extended-bin`, `xcrush-bin`)
4. Guide you through adding GitHub secrets

## What's Already Prepared

The following repositories are ready to push:

### Homebrew Tap
- **Location**: `/home/aleks/work/personal/crush_dev/homebrew-tap`
- **Will be**: `github.com/aleksclark/homebrew-tap`
- **Contents**: README.md, LICENSE, Formula/ directory

### AUR Packages

#### crush-extended-bin
- **Location**: `/home/aleks/work/personal/crush_dev/aur-packages/crush-extended-bin`
- **Will be**: `aur.archlinux.org/crush-extended-bin.git`
- **Contents**: PKGBUILD, .SRCINFO

#### xcrush-bin
- **Location**: `/home/aleks/work/personal/crush_dev/aur-packages/xcrush-bin`
- **Will be**: `aur.archlinux.org/xcrush-bin.git`
- **Contents**: PKGBUILD, .SRCINFO

## Manual Setup (If Script Fails)

### Step 1: Create Homebrew Tap Repository

```bash
cd /home/aleks/work/personal/crush_dev/homebrew-tap

# Option A: Using GitHub CLI
gh repo create aleksclark/homebrew-tap --public \
  --description "Homebrew tap for crush-modules" \
  --source=.

# Option B: Manual
# 1. Go to https://github.com/new
# 2. Repository name: homebrew-tap
# 3. Description: Homebrew tap for crush-modules
# 4. Make it public
# 5. Don't initialize with README

# Push to GitHub
git branch -M main
git remote add origin git@github.com:aleksclark/homebrew-tap.git
git push -u origin main
```

### Step 2: Set Up AUR SSH Access

```bash
# Generate SSH key for AUR (if you don't have one)
ssh-keygen -t ed25519 -C "aleks.clark@gmail.com" -f ~/.ssh/aur

# Display public key
cat ~/.ssh/aur.pub

# Add this key to your AUR account:
# 1. Go to: https://aur.archlinux.org/account/
# 2. Paste the public key into "SSH Public Key" section
# 3. Save

# Configure SSH
cat >> ~/.ssh/config << 'EOF'

# AUR
Host aur.archlinux.org
    IdentityFile ~/.ssh/aur
    User aur
EOF

# Test connection
ssh -T aur@aur.archlinux.org
# Should see: "Hi username, You've successfully authenticated..."
```

### Step 3: Push to AUR

```bash
# crush-extended-bin
cd /home/aleks/work/personal/crush_dev/aur-packages/crush-extended-bin
git remote add aur ssh://aur@aur.archlinux.org/crush-extended-bin.git
git push aur master

# xcrush-bin
cd /home/aleks/work/personal/crush_dev/aur-packages/xcrush-bin
git remote add aur ssh://aur@aur.archlinux.org/xcrush-bin.git
git push aur master
```

### Step 4: Configure GitHub Secrets

Go to: https://github.com/aleksclark/crush-modules/settings/secrets/actions

#### Add HOMEBREW_TAP_GITHUB_TOKEN

1. Create token at: https://github.com/settings/tokens
2. Click "Generate new token (classic)"
3. Name: "GoReleaser Homebrew"
4. Select scopes: `repo` (all), `workflow`
5. Click "Generate token"
6. Copy the token
7. In repository secrets, click "New repository secret"
8. Name: `HOMEBREW_TAP_GITHUB_TOKEN`
9. Value: Paste the token
10. Click "Add secret"

#### Add AUR_SSH_KEY

1. Get your private key:
   ```bash
   cat ~/.ssh/aur
   ```

2. Copy the ENTIRE output (including BEGIN/END lines)

3. In repository secrets, click "New repository secret"
4. Name: `AUR_SSH_KEY`
5. Value: Paste the entire private key
6. Click "Add secret"

#### Using GitHub CLI (Alternative)

```bash
cd /home/aleks/work/personal/crush_dev/crush-modules

# Add HOMEBREW_TAP_GITHUB_TOKEN
echo "YOUR_TOKEN_HERE" | gh secret set HOMEBREW_TAP_GITHUB_TOKEN

# Add AUR_SSH_KEY
cat ~/.ssh/aur | gh secret set AUR_SSH_KEY
```

## Verification

### Check Secrets

```bash
gh secret list
# Should show:
# AUR_SSH_KEY
# HOMEBREW_TAP_GITHUB_TOKEN
```

Or visit: https://github.com/aleksclark/crush-modules/settings/secrets/actions

### Check Repositories

```bash
# Homebrew tap
curl -s https://api.github.com/repos/aleksclark/homebrew-tap | jq .name

# AUR packages (after first release)
curl -s https://aur.archlinux.org/rpc/v5/info/crush-extended-bin | jq .results[].Name
curl -s https://aur.archlinux.org/rpc/v5/info/xcrush-bin | jq .results[].Name
```

## Test the Setup

Once everything is configured:

```bash
cd /home/aleks/work/personal/crush_dev/crush-modules

# Trigger a test release
git add .
git commit -m "chore: test automated release setup"
git push origin master

# Watch the workflow
# https://github.com/aleksclark/crush-modules/actions
```

After ~5-10 minutes:

### Test Homebrew Installation
```bash
brew tap aleksclark/tap
brew install crush-extended --dry-run
```

### Test AUR Installation
```bash
yay -Ss crush-extended-bin
# Should show the package
```

### Check GitHub Release
Visit: https://github.com/aleksclark/crush-modules/releases

## Troubleshooting

### Homebrew Formula Not Created

**Symptom**: No formulas in homebrew-tap repository after release

**Check**:
1. GitHub Actions logs for errors
2. Verify `HOMEBREW_TAP_GITHUB_TOKEN` secret exists and has correct permissions
3. Ensure homebrew-tap repository is public

**Fix**:
```bash
# Regenerate token with correct scopes
# repo (all), workflow
```

### AUR Package Not Updated

**Symptom**: AUR package shows old version or doesn't exist

**Check**:
1. GitHub Actions logs for AUR-related errors
2. Verify `AUR_SSH_KEY` is the correct private key
3. Test SSH connection: `ssh -T aur@aur.archlinux.org`

**Fix**:
```bash
# Test SSH connection manually
ssh -T aur@aur.archlinux.org

# If it fails, regenerate key and add to AUR
ssh-keygen -t ed25519 -C "aleks.clark@gmail.com" -f ~/.ssh/aur
cat ~/.ssh/aur.pub
# Add to https://aur.archlinux.org/account/
```

### Permission Denied (GitHub)

**Symptom**: `Permission denied (publickey)` when pushing

**Fix**:
```bash
# Check SSH key is added to GitHub
ssh -T git@github.com

# If fails, add key at:
# https://github.com/settings/keys
```

### AUR Repository Doesn't Exist

**Symptom**: `fatal: Could not read from remote repository`

**Cause**: AUR repositories don't exist until first push

**Fix**: This is normal - the first push creates the repository. Make sure:
1. You're authenticated with AUR
2. The package name is available
3. You own the package name (or it's unclaimed)

## Support

For issues with:
- This setup: https://github.com/aleksclark/crush-modules/issues
- Homebrew: https://github.com/Homebrew/brew/issues
- AUR: https://wiki.archlinux.org/title/AUR
