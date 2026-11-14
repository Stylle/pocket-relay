# Maintenance contracts

Run `python scripts/check-maintenance.py` before submitting a change. It checks Go formatting, JavaScript syntax and the versioned source hashes in `docs/shared-components.json`. After an intentional shared-source update, increment its recorded version and run `python scripts/check-maintenance.py --write` to refresh mirrors and hashes; review the diff.

Firmware Hub and Today Random Movie currently vendor the same portablebackup implementation and restore-wait helper. They retain independent repositories and application-specific validation. A shared fix must be applied to both, then their unit and portable-restore tests must pass. Do not unify authentication, databases or application permissions. UI dictionaries only translate registered interface text, never message or movie content.

## Compatibility evidence

CI exercises Linux; Gallery additionally exercises Windows. Published amd64/arm64 images are build targets; true iPad Safari, Android, macOS and physical ARM64 NAS validation must be recorded separately. Record OS/browser/version, navigation, upload/download, clipboard, focus, language/theme, 200% zoom, long names, network loss and data persistence. Do not mark an untested platform passed.

## Reverse proxies

Plain HTTP LAN deployment remains supported. When terminating HTTPS at a reverse proxy, set `PUBLIC_HTTPS=1` for a site that is exclusively accessed over HTTPS, or configure `TRUSTED_PROXIES` with the exact peer IP/CIDR and have that proxy replace (not append untrusted client values to) `X-Forwarded-Proto` and `X-Real-IP`. Leave this unset for direct deployment; never trust arbitrary forwarded headers. Forward the original Host. Do not enable PUBLIC_HTTPS for ordinary HTTP access, where Secure cookies cannot be sent.

## Release checks

Use independent tags and keep each existing Docker repository. Run tests/build, backup/export-import in a disposable second instance, verify original accounts and files, non-root container startup and persistence. Keep the complete data directory and matching key. Never delete volumes for a migration. No report claims a completed deep security scan until the scanner actually succeeds.

Gallery image publication can use an authenticated local Docker CLI after CI succeeds. The optional manual release workflow requires Docker Hub Actions secrets and a selected version tag.
