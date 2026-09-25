# Budgie BBS on DigitalOcean

The most beginner-friendly option (great docs, simple console, easy snapshots).
A **$6/mo Basic Droplet** (1 vCPU / 1 GB / 25 GB) is more than enough.

## 1. Create the Droplet

Console → **Create → Droplets**:

- **Image:** Ubuntu 24.04.
- **Type:** Basic / Regular, the $6 size.
- **Advanced → Add Initialization scripts (user data):** paste
  [`cloud-init.yaml`](cloud-init.yaml).
- **SSH key:** add yours.

First boot runs the provisioner (Caddy + systemd unit + ufw).

## 2. Point DNS

A record for your domain → the Droplet IP. Let it resolve before deploying.

## 3. Deploy the app

```bash
BUDGIE_HOST=root@<droplet-ip> BUDGIE_DOMAIN=bbs.example.com ./deploy/digitalocean/deploy.sh
```

Cross-compiles + ships the binary and SPA, wires the domain into Caddy +
`BUDGIE_PUBLIC_URL`, and starts the service. Re-run to update.

- Web: `https://bbs.example.com` (first user = admin).
- SSH TUI: `ssh -p 2222 <handle>@bbs.example.com`

## Notes

- **Firewall:** the cloud-init uses host `ufw`. If you attach a DO **Cloud
  Firewall**, also allow inbound `80`, `443`, `2222`.
- **Outbound SMTP (port 25) is blocked** on DigitalOcean (they don't unblock it).
  Email verification is optional; use an SMTP relay if you want it.
- **Backups:** enable Droplet backups, and back up `/var/lib/budgie` separately.
