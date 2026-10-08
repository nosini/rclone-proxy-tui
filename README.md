# rclone-proxy-tui

A terminal UI for [rclone](https://rclone.org) that does two things:

1. **Connects to upstream remotes like normal.** Your remotes are the ones in
   your regular `rclone.conf`. Add or edit them in the TUI, or press `c` to drop
   into rclone's own `rclone config`. Crypt, alias, union and the rest all keep
   working.
2. **Re-exposes those remotes to clients with a few key presses.** Pick
   remotes, press `s`, press enter. You get a client login with a generated
   password that works over **WebDAV, SFTP, S3, FTP and HTTP**. The same remote
   can be shared with as many clients as you like.

It's pure terminal and is built to be managed over SSH. A small background
daemon keeps the shares running after you close the TUI or log out.

```
rclone-proxy-tui  1 Remotes  2 Clients  3 Server      ● daemon  ● webdav:8080  ● sftp:2022  ● s3:8333
────────────────────────────────────────────────────────────────────────────────────────────────────
     NAME                   TYPE         SHARED WITH            POINTS AT
 [x] gdrive                 drive        Laptop
 [x] vault                  crypt        Laptop, Phone          → gdrive:encrypted
 [ ] nas                    sftp         -                      me@10.0.0.2
```

## Install

Install [rclone](https://rclone.org/install/) first, then download the binary
for your machine from [GitHub Releases](https://github.com/nosini/rclone-proxy-tui/releases/latest).
You do not need Go installed.

For Linux x86-64 (most PCs and servers):

```sh
curl -fL https://github.com/nosini/rclone-proxy-tui/releases/latest/download/rclone-proxy-tui-linux-amd64 -o rclone-proxy-tui
sudo install -m 755 rclone-proxy-tui /usr/local/bin/rclone-proxy-tui
```

For Linux ARM64, use `rclone-proxy-tui-linux-arm64` in the download URL.
Each release also includes a `SHA256SUMS` file.

## Quick start

```sh
rclone-proxy-tui
```

1. **Remotes tab (`1`)**: your existing remotes are listed. Press `n` to add
   one. The form is generated from rclone's own option list, so every
   backend works. For OAuth backends (Google Drive, OneDrive, Dropbox…) the
   terminal is handed to rclone for the login. Over SSH, answer `n` to
   *"Use web browser to automatically authenticate?"* and run the
   `rclone authorize` command it prints on any machine with a browser.
2. **Share**: press `space` on each remote you want to share, then `s`, then
   `enter`. The new client's username, password, URLs and ready-to-paste
   client rclone config pop up. The daemon is started automatically if it
   isn't running.
3. **Server tab (`3`)**: press `enter` on *Start on boot* to install a systemd
   service, so the shares survive logouts and reboots.

### Reusing remotes

Each client sees its remotes as top-level folders (S3 clients see them as
buckets). There are three ways to give remotes to clients:

- **Remotes tab**: select remotes with `space`. Press `s` to create a *new*
  client for them, or `a` to *add* them to an existing client.
- **Clients tab**: press `e` on a client to get a checklist of every remote.
  Press `p` on a remote to share only a sub-folder of it.
- The *SHARED WITH* column shows who has access to what.

### Keys

| Where   | Key | Action |
|---------|-----|--------|
| anywhere | `1` `2` `3` / `tab` | switch tab |
| anywhere | `?` | help |
| anywhere | `q` | quit (the daemon keeps running) |
| Remotes | `space` / `esc` | select / clear selection |
| Remotes | `s` | share selection with a new client |
| Remotes | `a` | add selection to an existing client |
| Remotes | `n` `e` `d` | new / edit / delete remote |
| Remotes | `t` | test (lists the top level) |
| Remotes | `o` | re-run OAuth login |
| Remotes | `c` | open `rclone config` |
| Clients | `enter` | login + connection info (`y` copies the password via OSC 52, `w` saves it to a file) |
| Clients | `n` `e` `d` | new / edit / delete client |
| Clients | `p` | generate a new password |
| Clients | `x` | enable / disable client |
| Server | `enter` | change setting, start/stop daemon, edit port |
| Server | `space` | enable / disable protocol |
| Server | `f` | extra `rclone serve` flags (e.g. `--read-only`) |
| Server | `R` | restart daemon |
| Server | `l` | view daemon log |

In remote forms: `◂ ▸` cycles through suggested values, `ctrl+g` generates a
password, `ctrl+r` reveals it, `ctrl+a` shows advanced options and `ctrl+s`
saves.

## How it works

```
 client ──WebDAV/SFTP/S3/FTP/HTTP──▶ rclone serve <proto> --auth-proxy "rclone-proxy-tui auth-proxy <proto>"
                                         │  on login: {"user","pass"} ─▶ auth proxy checks state.json
                                         │  ◀─ {"type":"combine","upstreams":"gdrive=gdrive: vault=vault:"}
                                         ▼
                              named remotes from your rclone.conf (crypt, drive, sftp, …)
```

- The **daemon** (`rclone-proxy-tui daemon`) runs one `rclone serve` process per
  enabled protocol, on one port each. It watches the state file and rclone
  config, picks up changes within a second, restarts crashed servers with backoff, and writes
  `status.json` for the TUI.
- **Per-client logins** use rclone's `--auth-proxy`. On each login rclone runs
  `rclone-proxy-tui auth-proxy <proto>`, which checks the password and answers
  with a `combine` backend whose folders are that client's remotes. The
  upstreams are *named* remotes from `rclone.conf`, so crypt and other
  wrapping remotes work exactly as they do on the command line. S3 uses the
  username as the access key ID and the password as the secret.
- **Revoking** a client (delete, disable, new password, fewer remotes)
  restarts the servers, because rclone caches logins. Before any restart or
  shutdown, the daemon asks each server (over a private rc socket) whether
  uploads are still queued in its VFS cache, and waits up to 2 minutes (60s
  on shutdown) for them to finish. Changes can therefore take up to 2 minutes
  to apply, and existing sessions may retain access while uploads drain. If the drain times out or a server
  crashes, queued uploads may need to be sent again.

## Files

Everything lives in `~/.config/rclone-proxy-tui/` (override with `--dir` or
`$RCLONE_PROXY_TUI_DIR`):

| File | |
|------|-|
| `state.json` | clients (passwords in plain text, mode 0600) and server settings |
| `status.json` | written by the daemon |
| `daemon.log` | daemon and rclone server output (rotated at 10 MB) |
| `clients/<user>.txt` | connection info saved with `w` |

Your remotes stay in your normal `rclone.conf`. The TUI records which config
file and rclone binary it found on first run, so a systemd service uses the
same ones.

## Command line

```
rclone-proxy-tui                    open the TUI
rclone-proxy-tui daemon             run the daemon in the foreground (for custom init systems / containers)
rclone-proxy-tui status             print daemon and server status
rclone-proxy-tui install-service    install + start the systemd service (user unit, or system unit as root)
rclone-proxy-tui uninstall-service
```

As a normal user, `install-service` writes a *user* unit and enables
lingering (`loginctl enable-linger`) so the service keeps running after you
log out. If lingering can't be enabled without root, the TUI tells you the
command to run. As root it installs a system unit.

## Notes and limits

- **Exposure.** By default the servers listen on all interfaces (`0.0.0.0`).
  Set *Listen address* to a VPN/Tailscale IP to limit that. Add a TLS
  certificate and key in the Server tab to serve WebDAV/S3/HTTP over https
  and FTP as FTPS. SFTP is always encrypted. Plain FTP and HTTP send
  passwords in the clear.
- **FTP passive mode** uses ports 30000-32000 by default. Behind NAT, add
  `--passive-port` / `--public-ip` as extra flags.
- **S3**: per-client logins need a recent rclone with `--auth-proxy` on
  `serve s3`. On older versions the Server tab shows an error for S3 and the
  other protocols still work. Remote names become bucket names, so keep them
  simple (lowercase, no spaces) for strict S3 clients.
- **SFTP** logins are password only (no public keys).
- **Encrypted rclone.conf** (`RCLONE_CONFIG_PASS`): the daemon needs the
  password in its environment, e.g. via an `EnvironmentFile=` in a systemd
  drop-in.
- Renaming remotes isn't supported in the TUI; use `c` (rclone config).
- Linux is the main target. macOS should work without systemd support.
  Windows is not supported.

See [the development guide](docs/development.md) for building and testing.
