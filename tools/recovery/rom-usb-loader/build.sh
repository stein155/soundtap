#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build and test the loader in a container. Takes make targets; defaults to `test`,
# which builds everything and runs every self-check that needs no hardware.
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
docker build --quiet --tag soundtap-rom-usb-loader:bookworm \
        --file "$here/Dockerfile.loader" "$here" >/dev/null
# Root inside the container, because test_loader.sh refuses to run as anyone else:
# the loader itself insists on being one root process. The container is the boundary.
exec docker run --rm \
        --volume "$here:/src" \
        --workdir /src \
        soundtap-rom-usb-loader:bookworm \
        make "$@"
