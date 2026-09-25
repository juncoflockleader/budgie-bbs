# Budgie BBS on Hetzner Cloud

Best price/performance for a single-host Budgie. A **CAX11** (2 Ampere ARM vCPU /
4 GB / 40 GB, ~€3.8/mo) is plenty — Budgie runs happily on a low-power Mac mini.

## 1. Create the server

In the Hetzner Cloud console → **Add Server**:

- **Location:** any (Ashburn/Hillsboro for US, Falkenstein/Helsinki/Nuremberg for EU).
- **Image:** Ubuntu 24.04.
- **Type:** `CAX11` (ARM, cheapest) — or `CX22` for x86.
- **Cloud config:** paste [`cloud-init.yaml`](cloud-init.yaml).
- **SSH key:** add yours (you'll deploy over SSH as `root`).

First boot runs the provisioner (Caddy + the systemd unit + ufw). It takes a
minute or two.

## 2. Point DNS

Create an **A record** (and AAAA if you enabled IPv6) for your domain → the
server's IP. Wait for it to resolve before deploying, or Caddy can't get a
certificate.

## 3. Deploy the app

From your workstation, inside the repo:

```bash
# arm64 for CAX, amd64 for CX
BUDGIE_HOST=root@<server-ip> BUDGIE_DOMAIN=bbs.example.com ./deploy/hetzner/deploy.sh
```

This cross-compiles the static binary + builds the SPA, ships them, wires your
domain into Caddy + `BUDGIE_PUBLIC_URL`, and starts the service. Re-run it any
time to ship an update (data + sessions persist).

- Web: `https://bbs.example.com` — first registered user becomes admin.
- SSH TUI: `ssh -p 2222 <handle>@bbs.example.com`

## Notes

- **Firewall:** the cloud-init opens ports with host-level `ufw`. If you also
  attach a Hetzner **Cloud Firewall**, allow inbound `80`, `443`, and `2222`
  there too.
- **Outbound SMTP (port 25) is blocked** by Hetzner by default (request an
  unblock via support if you need direct mail). Email verification is optional;
  if you want it, use an SMTP relay — see `../README.md` and
  `deployment-single-node.md`.
- **Backups:** enable Hetzner snapshots/backups, but also back up
  `/var/lib/budgie` (the SQLite DB + JWT secret are the entire state).
