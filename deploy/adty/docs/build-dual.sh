#!/usr/bin/env bash
# Dual-output VitePress build script for ADTY AI:
#   1) dist-root    (base="/")     -> standalone docs domain (e.g. https://docs.adtyai.com/)
#   2) dist-subpath (base="/docs/") -> origin ADTY_DOCS_DIR (e.g. /opt/adtyai/data/docs/)
#
# Optional sync to origin:
#   ORIGIN_DOCS_RSYNC="root@<origin-ip>:/opt/adtyai/data/docs/" bash build-dual.sh
set -euo pipefail

DOCS_SRC="${DOCS_SRC:-docs}"
OUT_ROOT="${OUT_ROOT:-dist-root}"
OUT_SUBPATH="${OUT_SUBPATH:-dist-subpath}"

echo "[1/2] Building standalone root docs (base=/ -> ${OUT_ROOT})..."
VITEPRESS_BASE="/" VITEPRESS_OUT="../${OUT_ROOT}" npx vitepress build "${DOCS_SRC}"

echo "[2/2] Building same-domain white-label docs (base=/docs/ -> ${OUT_SUBPATH})..."
VITEPRESS_BASE="/docs/" VITEPRESS_OUT="../${OUT_SUBPATH}" npx vitepress build "${DOCS_SRC}"

if [ -n "${ORIGIN_DOCS_RSYNC:-}" ]; then
  echo "[sync] Syncing ${OUT_SUBPATH}/ -> ${ORIGIN_DOCS_RSYNC}"
  rsync -av --delete "${OUT_SUBPATH}/" "${ORIGIN_DOCS_RSYNC}"
fi

echo "Done."
