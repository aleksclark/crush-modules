# Quick Reference: Automated Releases

## How It Works

```
Push to master → Auto-tag (CalVer) → Build → Distribute
     ↓
  5-10 min
     ↓
✅ GitHub Releases
✅ Homebrew (aleksclark/tap)  
✅ AUR (crush-extended-bin, xcrush-bin)
✅ .deb, .rpm packages
```

## Release a New Version

```bash
# That's it! Just push to master
git push origin master
```

Tag generated automatically: `YYYY.MM.DD.BUILD`

## Install After Release

### Homebrew
```bash
brew tap aleksclark/tap
brew install crush-extended
```

### AUR
```bash
yay -S crush-extended-bin
```

### Debian/Ubuntu
```bash
curl -LO https://github.com/aleksclark/crush-modules/releases/latest/download/crush-extended_*_linux_x86_64.deb
sudo dpkg -i crush-extended_*.deb
```

## Setup Required (One-Time)

Before the first release:

1. **Create Homebrew tap**: `aleksclark/homebrew-tap` repository
2. **Initialize AUR packages**: `crush-extended-bin`, `xcrush-bin`
3. **Add GitHub secrets**:
   - `HOMEBREW_TAP_GITHUB_TOKEN` (Personal Access Token with repo scope)
   - `AUR_SSH_KEY` (SSH private key for AUR)

See [docs/SETUP.md](./SETUP.md) for detailed instructions.

## Package Names

| Binary | GitHub | Homebrew | AUR | Conflicts |
|--------|--------|----------|-----|-----------|
| crush-extended | ✅ | crush-extended | crush-extended-bin | crush |
| xcrush | ✅ | xcrush | xcrush-bin | - |

## Warnings

All packages include prominent warnings:
> ⚠️ UNOFFICIAL, UNTESTED BUILD  
> This is NOT an official Charm Labs release. Use at your own risk.

## Troubleshooting

Check GitHub Actions logs if:
- Homebrew formula not updated → Verify `HOMEBREW_TAP_GITHUB_TOKEN`
- AUR package not updated → Verify `AUR_SSH_KEY` and SSH access
- Build failed → Check crush-plugin-poc checkout and Go version

Test locally:
```bash
./scripts/prebuild.sh
goreleaser release --snapshot --clean --skip=publish
```
