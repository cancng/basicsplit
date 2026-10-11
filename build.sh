#!/bin/sh
# macOS: builds build/basicsplit.app. Needs Go and Xcode.
#
#   ./build.sh                 for this machine, ad-hoc signed
#   UNIVERSAL=1 ./build.sh     Apple Silicon + Intel
#   VERSION=1.2.0 ./build.sh   version number
#   SIGN_IDENTITY="Developer ID Application: ..." ./build.sh
set -e
cd "$(dirname "$0")"

APP=build/basicsplit.app
VERSION="${VERSION:-0.0.0}"
SIGN="${SIGN_IDENTITY:--}"

rm -rf "$APP" build/icon.iconset
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# engine: the Go program that runs as root. -trimpath makes the output
# reproducible, so an unchanged engine does not ask to be installed again.
engine() {
	CGO_ENABLED=0 GOOS=darwin GOARCH="$1" go build -trimpath -o "$2" .
}
if [ -n "$UNIVERSAL" ]; then
	engine arm64 build/engine-arm64
	engine amd64 build/engine-amd64
	lipo -create -output "$APP/Contents/MacOS/basicsplit-engine" build/engine-arm64 build/engine-amd64
	rm build/engine-arm64 build/engine-amd64
	set -- --arch arm64 --arch x86_64
else
	engine "$(go env GOARCH)" "$APP/Contents/MacOS/basicsplit-engine"
	set --
fi

# interface: the menu bar icon and the settings window
swift build -c release --package-path macos "$@"
cp "$(swift build -c release --package-path macos "$@" --show-bin-path)/basicsplit" "$APP/Contents/MacOS/basicsplit"

cp macos/Info.plist "$APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$APP/Contents/Info.plist"

swift macos/icon.swift build/icon.iconset
iconutil -c icns -o "$APP/Contents/Resources/AppIcon.icns" build/icon.iconset
rm -rf build/icon.iconset

# notarization wants a real certificate, the hardened runtime and a timestamp
if [ "$SIGN" = "-" ]; then
	set -- --force --sign -
else
	set -- --force --sign "$SIGN" --options runtime --timestamp
fi
codesign "$@" "$APP/Contents/MacOS/basicsplit-engine"
codesign "$@" "$APP"

echo "$APP is ready (version $VERSION)."
