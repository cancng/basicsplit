#!/bin/sh
# Release package: a signed and notarized zip plus the Homebrew cask file.
#
#   ./release.sh 0.0.1
#
# One-time setup:
#   1. A "Developer ID Application" certificate in the keychain
#      (developer.apple.com > Certificates).
#   2. Stored credentials for notarization:
#        xcrun notarytool store-credentials basicsplit \
#          --apple-id <apple-id> --team-id <team-id> --password <app-specific-password>
set -e
cd "$(dirname "$0")"

VERSION="$1"
if [ -z "$VERSION" ]; then
	echo "usage: ./release.sh <version>" >&2
	exit 1
fi
PROFILE="${NOTARY_PROFILE:-basicsplit}"
IDENTITY="${SIGN_IDENTITY:-$(security find-identity -v -p codesigning |
	sed -n 's/.*"\(Developer ID Application:[^"]*\)".*/\1/p' | head -1)}"
if [ -z "$IDENTITY" ]; then
	echo "No 'Developer ID Application' certificate in the keychain." >&2
	exit 1
fi

VERSION="$VERSION" SIGN_IDENTITY="$IDENTITY" UNIVERSAL=1 ./build.sh

APP=build/basicsplit.app
ZIP="build/basicsplit-$VERSION.zip"

# send it to Apple, staple the ticket to the app, then make the zip to ship
ditto -c -k --keepParent "$APP" "$ZIP"
xcrun notarytool submit "$ZIP" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$APP"
rm "$ZIP"
ditto -c -k --keepParent "$APP" "$ZIP"
spctl --assess --type execute -v "$APP"

SHA="$(shasum -a 256 "$ZIP" | cut -d' ' -f1)"
sed -e "s/@VERSION@/$VERSION/" -e "s/@SHA256@/$SHA/" packaging/basicsplit.rb.in > build/basicsplit.rb

cat <<END

$ZIP is ready.

To publish:
  gh release create "v$VERSION" "$ZIP" --title "v$VERSION" --generate-notes
  save build/basicsplit.rb as Casks/basicsplit.rb in the tap repository
END
