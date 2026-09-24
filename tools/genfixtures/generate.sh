#!/usr/bin/env bash
# Regenerates the image test fixtures. Needs libvips with an HEVC *encoder*, which the
# production image deliberately lacks, so run it in a throwaway container from the repo root:
#
#   docker run --rm -v "$PWD:/src" -w /src golang:1.26-trixie bash tools/genfixtures/generate.sh
set -euo pipefail

apt-get update -qq
apt-get install -y -qq --no-install-recommends \
	libvips-dev libheif-plugin-x265 libheif-plugin-libde265 libimage-exiftool-perl >/dev/null

go run ./tools/genfixtures

cd internal/imageproc/testdata
exiftool -q -overwrite_original -n -Orientation=6 orientation6.jpg
exiftool -q -overwrite_original \
	-Make=FixtureCam -Model="Fixture Phone 1" -DateTimeOriginal="2024:01:02 03:04:05" \
	-GPSLatitude=25.0330 -GPSLatitudeRef=N -GPSLongitude=121.5654 -GPSLongitudeRef=E \
	gps.jpg
exiftool -q -S -Orientation -n orientation6.jpg
exiftool -q -S -GPSPosition gps.jpg
