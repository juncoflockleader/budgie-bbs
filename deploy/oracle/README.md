# Budgie BBS on Oracle Cloud (Always Free)

Genuinely **free** forever: the Ampere A1 "Always Free" shape gives you up to
4 ARM vCPU / 24 GB RAM — wildly more than Budgie needs. The trade-offs are
account/billing friction and flaky capacity (A1 instances are often "out of
capacity" in popular regions; retry, or pick a quieter region/AD).

## 1. Create the instance

Console → **Compute → Instances → Create**:

- **Image:** Canonical Ubuntu 24.04.
- **Shape:** Ampere `VM.Standard.A1.Flex` (e.g. 1 OCPU / 6 GB stays in the free
  tier).
- **Add your SSH public key.**
- **Show advanced options → Management → cloud-init script:** paste
  [`cloud-init.yaml`](cloud-init.yaml).

## 2. Open the ports in TWO places ⚠️

This is the Oracle gotcha. Opening the host firewall is not enough.

1. **Host firewall** — handled by the cloud-init (it adds iptables ACCEPT rules
   for 80/443/2222 and persists them).
2. **VCN Security List / NSG** — you must add ingress rules yourself in the
   console: **Networking → Virtual Cloud Networks → your VCN → Security Lists →
   Default → Add Ingress Rules**, source `0.0.0.0/0`, TCP, for **80**, **443**,
   and **2222**. Until you do this, nothing reaches the instance.

## 3. Point DNS

A record for your domain → the instance's public IP. Let it resolve.

## 4. Deploy the app

The default SSH user on OCI Ubuntu images is `ubuntu` (passwordless sudo):

```bash
BUDGIE_HOST=ubuntu@<instance-ip> BUDGIE_DOMAIN=bbs.example.com ./deploy/oracle/deploy.sh
```

- Web: `https://bbs.example.com` (first user = admin).
- SSH TUI: `ssh -p 2222 <handle>@bbs.example.com`

## Notes

- **Don't lose the instance:** Oracle has reclaimed idle Always-Free compute in
  the past. Keep a backup of `/var/lib/budgie` (the SQLite DB + JWT secret).
- **Outbound SMTP (port 25) is blocked.** Email verification is optional; use a
  relay if you want it.
