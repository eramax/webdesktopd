#!/bin/sh
# release.sh – bump minor version, tag, and push.
# Usage: ./release.sh "Changelog / release description"
set -e

DESCRIPTION="${1:?Usage: ./release.sh \"release description\"}"

# Find the highest vMAJOR.MINOR tag.
LATEST=$(git tag --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+$' | head -1)

if [ -z "$LATEST" ]; then
    NEXT="v0.1"
else
    MAJOR=$(echo "$LATEST" | cut -d. -f1 | tr -d 'v')
    MINOR=$(echo "$LATEST" | cut -d. -f2)
    NEXT="v${MAJOR}.$((MINOR + 1))"
fi

echo "Latest tag : ${LATEST:-none}"
echo "Next tag   : $NEXT"
echo "Description: $DESCRIPTION"
echo ""

# Push the current branch first.
git push

# Create an annotated tag — the message becomes the GitHub Release body.
git tag -a "$NEXT" -m "$DESCRIPTION"
git push origin "$NEXT"

echo ""
echo "Released $NEXT — workflow will build and publish packages."
