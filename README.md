# webdesktopd

`webdesktopd` is a browser-based Linux desktop for a single Unix user. It reuses SSH credentials for login, keeps terminal sessions alive across browser disconnects, and multiplexes terminal, file, and proxy traffic over one WebSocket.

## Features

- Real terminal tabs backed by PTYs, with reconnect and output replay
- File manager with directory listing, upload, download, rename, delete, and chmod
- Port proxy for local web apps under `/_proxy/{port}/...`
- Desktop shell with windows, docking, and saved UI state
- System stats panel for CPU, memory, disk, network, uptime, and processes
- SSH password or SSH key authentication against the local `sshd`
- Login form with `Remember me` support that restores the username and secret from browser IndexedDB
- Debian/Ubuntu `.deb` and Alpine `.apk` release packages

## Install

### From release packages

1. Download the latest `.deb` or `.apk` from the GitHub Releases page.
2. Debian / Ubuntu:
   - `sudo dpkg -i webdesktopd_*.deb`
3. Alpine:
   - `sudo apk add ./webdesktopd-*.apk`
4. Set a strong `JWT_SECRET` in `/etc/webdesktopd/env`.
5. Start the service:
   - systemd: `sudo systemctl start webdesktopd`
   - OpenRC: `sudo rc-service webdesktopd start`


### From source

1. Install Go 1.26+ and Bun.
2. Build the frontend and backend:
   - `make build`
3. Run locally:
   - `JWT_SECRET=dev-secret ./webdesktopd`
   - or `make run`

## Development

- `make dev-backend` starts the Go server on `:8080`
- `make dev-frontend` starts the Vite dev server on `:5173`
- `make test` runs Go unit tests
- `make e2e` runs browser and server integration tests
- `make build` builds the frontend and backend
- `make deploy PASS=... REMOTE_USER=abb` builds and deploys to the remote host
- `make tunnel PASS=... REMOTE_USER=abb` opens an SSH tunnel to the remote instance
