# GoReleaser Configuration for Crush Modules

This repository uses GoReleaser to build and release both `crush-extended` and `xcrush` binaries.

## Local Testing

### Build binaries locally:
```bash
./scripts/prebuild.sh
```

This creates:
- `dist/xcrush` - The build tool
- `dist/crush-extended_<os>_<arch>` - Extended crush binaries for all platforms

### Test goreleaser (requires goreleaser installed):
```bash
goreleaser release --snapshot --clean --skip=publish
```

## Release Process

### Automatic Releases (Recommended)

Every push to the `master` branch automatically triggers a release:

```bash
# Just commit and push to master
git add .
git commit -m "feat: add new feature"
git push origin master
```

**What happens automatically:**
1. GitHub Actions generates a CalVer tag (e.g., `2026.01.30.1`)
2. Builds crush-extended and xcrush for all platforms
3. Creates GitHub Release with binaries
4. Updates Homebrew formulas in `aleksclark/homebrew-tap`
5. Updates AUR packages (`crush-extended-bin`, `xcrush-bin`)

**Timeline:** ~5-10 minutes from push to packages available

### Manual Release (Alternative)

You can also trigger a release manually by creating a tag:

```bash
# CalVer format: YYYY.MM.DD.BUILD
git tag 2026.01.30.1
git push origin 2026.01.30.1
```

This is useful for creating specific version tags or when you want more control over the release timing.

### Versioning

We use CalVer (Calendar Versioning): `YYYY.MM.DD.BUILD`

- `YYYY`: 4-digit year
- `MM`: 2-digit month (zero-padded)
- `DD`: 2-digit day (zero-padded)  
- `BUILD`: Build number for the day (starting at 1)

Examples:
- `2026.01.30.1` - First build on January 30, 2026
- `2026.01.30.2` - Second build on January 30, 2026

### First-Time Setup

Before releases will work, you need to configure secrets and repositories. See [docs/SETUP.md](./docs/SETUP.md) for detailed setup instructions:

- Create `aleksclark/homebrew-tap` repository
- Initialize AUR packages (`crush-extended-bin`, `xcrush-bin`)
- Add GitHub secrets: `HOMEBREW_TAP_GITHUB_TOKEN`, `AUR_SSH_KEY`

## Package Details

### crush-extended
- **Name**: crush-extended
- **Description**: ⚠️ UNOFFICIAL, UNTESTED BUILD - Crush with community plugins
- **Conflicts with**: crush
- **Plugins included**: otlp, agent-status, periodic-prompts

### xcrush
- **Name**: xcrush
- **Description**: ⚠️ UNOFFICIAL TOOL - Build custom Crush distributions

## Installation

### From GitHub Releases
```bash
# Download and extract
curl -LO https://github.com/aleksclark/crush-modules/releases/latest/download/crush-extended_VERSION_OS_ARCH.tar.gz
tar xzf crush-extended_VERSION_OS_ARCH.tar.gz
sudo mv crush-extended /usr/local/bin/
```

### Debian/Ubuntu
```bash
# Download .deb package
curl -LO https://github.com/aleksclark/crush-modules/releases/latest/download/crush-extended_VERSION_linux_x86_64.deb
sudo dpkg -i crush-extended_VERSION_linux_x86_64.deb
```

### Fedora/RHEL
```bash
# Download .rpm package
curl -LO https://github.com/aleksclark/crush-modules/releases/latest/download/crush-extended_VERSION_linux_x86_64.rpm
sudo rpm -i crush-extended_VERSION_linux_x86_64.rpm
```

### Homebrew (if tap is configured)
```bash
brew tap aleksclark/tap
brew install crush-extended
```
