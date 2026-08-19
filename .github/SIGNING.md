# Release signing & notarization

Release binaries are code-signed with a Developer ID Application certificate
and notarized with Apple, from the Linux release runner (goreleaser's
`notarize` block, quill under the hood — no macOS runner involved). This
document covers the credentials that make that work: what they are, how to
(re)create them, and what happens if they leak.

## Secrets

All five live in the `release` GitHub environment (protected, `v*` tags only),
and are exposed only to the goreleaser step of `release.yml`. A preflight step
fails the release if they're missing, so a misconfiguration can't silently
ship unsigned binaries.

| Secret | Contents |
| --- | --- |
| `MACOS_SIGN_P12` | base64 of the Developer ID Application cert + private key (`.p12`) |
| `MACOS_SIGN_PASSWORD` | password protecting the `.p12` |
| `MACOS_NOTARY_ISSUER_ID` | App Store Connect API issuer ID (UUID) |
| `MACOS_NOTARY_KEY_ID` | App Store Connect API key ID |
| `MACOS_NOTARY_KEY` | base64 of the App Store Connect API key (`.p8`) |

## Setup / rotation

Team ID: `N58LV5U4Q3` (individual membership, "RYAN ANDREW LEWIS").

### 1. Developer ID Application certificate (.p12)

The cert lives in the login keychain (created via Xcode → Settings → Accounts
→ Manage Certificates). To export:

1. Keychain Access → login keychain → My Certificates.
2. Expand "Developer ID Application: RYAN ANDREW LEWIS (N58LV5U4Q3)", select
   the certificate **and** its private key, File → Export Items → `.p12`.
3. Choose a strong password; store it (and ideally the `.p12` itself) in the
   password manager.

If notarization later fails with an incomplete-chain error, re-export making
sure the Developer ID intermediate CA is included (quill normally
reconstructs the Apple chain itself, so this is unlikely).

### 2. App Store Connect API key (.p8)

Needed because quill authenticates to the notary API with an ASC API key —
Apple-ID + app-specific-password (the `notarytool store-credentials` route
used for local signing) does not work here.

1. [App Store Connect](https://appstoreconnect.apple.com) → Users and Access
   → Integrations → App Store Connect API → Team Keys → Generate API Key.
2. Role: **Developer** — the least-privileged role that can notarize. Do not
   use Admin.
3. Download the `.p8` (single chance), note the **Key ID** and the page-level
   **Issuer ID**. Store the `.p8` in the password manager.

### 3. Load the GitHub secrets

```sh
REPO=ryanlewis/things-cli
base64 -i DeveloperID.p12 | gh secret set MACOS_SIGN_P12 --env release --repo "$REPO"
gh secret set MACOS_SIGN_PASSWORD --env release --repo "$REPO"   # prompts
gh secret set MACOS_NOTARY_ISSUER_ID --env release --repo "$REPO" --body "<issuer-uuid>"
gh secret set MACOS_NOTARY_KEY_ID --env release --repo "$REPO" --body "<key-id>"
base64 -i AuthKey_<key-id>.p8 | gh secret set MACOS_NOTARY_KEY --env release --repo "$REPO"
```

Then delete any loose `.p12`/`.p8` copies from disk — the password manager
holds the canonical copies.

### 4. Verify after the first signed release

```sh
curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | INSTALL_DIR=/tmp/things-verify sh
codesign -dvv /tmp/things-verify/things            # expect the Developer ID identity + Team ID
codesign --verify --strict /tmp/things-verify/things
xcrun notarytool history --key AuthKey_<key-id>.p8 --key-id <key-id> --issuer <issuer-uuid>  # status: Accepted
```

Note: bare Mach-O binaries can't have the notarization ticket stapled
(stapling is app/dmg/pkg only), so Gatekeeper's first-run check does an online
lookup. That's fine for Homebrew/curl installs; it only bites a fully-offline
first run.

## Threat model — if a secret leaks

The release job is the exposure point (this is why its egress is locked down
and the secrets are step-scoped):

- **`.p12` + password**: an attacker can sign arbitrary software as
  "RYAN ANDREW LEWIS" until revoked. Revoke the certificate at
  [developer.apple.com → Certificates](https://developer.apple.com/account/resources/certificates/list),
  then mint a new one and rotate `MACOS_SIGN_P12`/`MACOS_SIGN_PASSWORD`.
  Apple can also revoke notarization tickets for known-malicious binaries.
- **ASC API key (Developer role)**: can submit notarizations under the
  account (no cert access, can't sign). Revoke it in App Store Connect →
  Integrations, generate a fresh key, rotate the three `MACOS_NOTARY_*`
  secrets.
- **`HOMEBREW_TAP_GITHUB_TOKEN`**: can push casks to `ryanlewis/homebrew-tap`
  only (fine-grained PAT). Revoke/rotate in GitHub settings.

None of these grant access to this repository's code or tags.
