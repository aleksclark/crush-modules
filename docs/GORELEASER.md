# GoReleaser Implementation Summary

## Overview

Implemented GoReleaser for automated releases of `crush-extended` and `xcrush` binaries with automatic distribution to:
- GitHub Releases
- Homebrew (`aleksclark/homebrew-tap`)
- AUR (Arch User Repository)

## Key Decisions

### Automatic Releases on Push to Master

Every push to `master` triggers an automatic release:
- CalVer tag auto-generated (e.g., `2026.01.30.1`)
- Binaries built for all platforms
- Packages distributed to all channels
- No manual intervention required

### Package Names & Branding

- **crush-extended**: Unofficial Crush build with community plugins
  - Clear warnings in description that this is unofficial and untested
  - Conflicts with official `crush` package
  - Maintainer: Aleks Clark <aleks.clark@gmail.com>

- **xcrush**: Build tool for creating custom Crush distributions
  - Also marked as unofficial
  - No conflicts (standalone tool)

### Build Strategy

Since each plugin is its own Go module, we use a two-phase build:

1. **Pre-build script** (`scripts/prebuild.sh`):
   - Builds xcrush from crush-plugin-poc
   - Uses xcrush to build crush-extended for all platforms
   - Creates binaries in `dist/` directory

2. **GoReleaser**:
   - Builds xcrush natively for all platforms
   - Packages pre-built crush-extended binaries into archives
   - Creates .deb and .rpm packages
   - Publishes to GitHub Releases

### Distribution Formats

#### Archives (.tar.gz)
- Linux: amd64, arm64
- macOS: amd64 (Intel), arm64 (Apple Silicon)

#### System Packages
- Debian/Ubuntu: .deb packages
- Fedora/RHEL: .rpm packages
- Both include architecture variants

#### Homebrew
- Repository: `aleksclark/homebrew-tap`
- Formulas: `crush-extended.rb`, `xcrush.rb`
- Auto-updated on every release

#### AUR (Arch Linux)
- Packages: `crush-extended-bin`, `xcrush-bin`
- Auto-updated on every release via SSH deployment

## Files Created

```
.goreleaser.yaml           # GoReleaser configuration (with AUR support)
scripts/
  ├── prebuild.sh          # Pre-build script for crush-extended
  └── build-crush-extended.sh  # Per-platform build helper (unused)
.github/workflows/
  └── release.yaml         # GitHub Actions workflow with auto-tagging
docs/
  ├── SETUP.md             # Repository and secrets setup guide
  └── GORELEASER.md        # This file
RELEASE.md                 # Release documentation
LICENSE                    # MIT license
.gitignore                 # Updated to ignore build artifacts
```

## Configuration Highlights

### Warning Messages

All packages include prominent warnings:

```
⚠️  UNOFFICIAL, UNTESTED BUILD ⚠️

This is NOT an official Charm Labs release.
Use at your own risk.
```

### Package Conflicts

crush-extended is marked as conflicting with:
- `crush` (official package)

This prevents installing both simultaneously.

### Release Header

GitHub releases include a warning header:

```markdown
## ⚠️  UNOFFICIAL BUILDS - USE AT YOUR OWN RISK

These are **NOT** official Charm Labs releases. These builds include 
experimental community plugins that have not been reviewed or tested 
by Charm Labs.
```

## Usage

### Trigger a Release

**Automatic (Recommended):**
```bash
# Just push to master
git add .
git commit -m "feat: new feature"
git push origin master
```

**Manual (Alternative):**
```bash
git tag 2026.01.30.1
git push origin 2026.01.30.1
```

The GitHub Actions workflow will automatically:
1. Generate CalVer tag (if pushing to master)
2. Build and publish to all channels

### Test Locally

```bash
# Build binaries
./scripts/prebuild.sh

# Test goreleaser (requires goreleaser installed)
goreleaser release --snapshot --clean --skip=publish
```

## Future Improvements

1. **Windows Support**: Add Windows builds (requires testing xcrush on Windows)
2. **Signatures**: Add GPG signing for packages
3. **Checksums**: Enhance checksum verification documentation
4. **Nix**: Add Nix flake support
5. **Snapcraft**: Add Snap package for universal Linux distribution
