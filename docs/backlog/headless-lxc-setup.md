# Setup option for a headless LXC container

Found on 2026-11-22 while building direct library mode (the user asked for it as a later task).
Not started. Delete this file when it is fixed or dropped.

## What is wanted

A supported way to install and run the daemon in a headless LXC container (Proxmox and the like):
no desktop, no user session, the library on a bind-mounted host folder, the web UI reached from
another machine.

## What exists today

- `install.sh` installs the release for the current user: binaries in `~/.local/bin` and a
  **systemd user service** (`infra/linux/autoanimedownloader.service`, `make install-user`). A
  user service needs a logged-in session or `loginctl enable-linger`; a fresh container usually
  only has root and no lingering user.
- The daemon already copes with no display: the tray starts only when `DISPLAY` is set
  (`cmd/daemon/main.go`), but the first boot still tries to open a browser tab.
- The Docker image (`docker/`) is the closest thing to headless today, but an LXC user usually
  wants the binary on the container itself, not Docker inside LXC.
- `PORT` defaults to `:8091` on all interfaces, which is what a remote browser needs.

## What to check

- A system-wide install mode in `install.sh` (`--system`): binaries in `/usr/local/bin`, a system
  service with `User=`/`Group=` and the data dir under that user's home or `/var/lib`, enabled at
  boot without linger.
- Skip the first-boot browser open when there is no display, and log the URL to open instead.
- Bind-mounted library permissions (LXC idmap): the service user's uid must be able to write the
  host folder. Direct library mode avoids the hardlink requirement, which helps with network or
  FUSE-backed mounts.
- Document it in `README.md` / `docs/guides/` next to the Docker instructions.
