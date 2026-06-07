# Troubleshooting

The two most common problems and how to fix them. For background on how clipfan
works, see the [README](../README.md) and [docs/ARCHITECTURE.md](ARCHITECTURE.md).

## The daemon isn't running

Clipboard sync needs the background daemon running on each host. Check it with the
health endpoint:

```sh
curl -s http://localhost:7853/v1/health        # → ok
```

If that prints `ok`, the daemon is up. If it errors or hangs, restart the user
service:

```sh
launchctl kickstart -k gui/$UID/com.primeradiant.clipfan      # macOS
systemctl --user restart clipfan                              # Linux
```

On macOS you can also use the menubar app: **Settings… → General → Developer**
exposes the daemon log and a restart button.

If it still won't come up, check the logs:

- **macOS:** `~/Library/Logs/clipfan.out.log` and `~/Library/Logs/clipfan.err.log`
- **Linux:** `systemctl --user status clipfan` / `journalctl --user -u clipfan`

## Peers aren't syncing

When a copy on one host doesn't land on another, work down this list:

1. **Confirm both daemons are healthy.** Run the health check above on each host.
   A peer can't sync if its daemon is down.

2. **Check the Fleet view.** Open the menubar icon → **Settings… → Fleet**. Each
   peer shows a colored health dot — green (synced), orange (offline), gray (idle)
   — and its last **↑ push / ↓ recv** times. An orange dot means the Mac can't
   currently reach that peer.

3. **Make sure the host is reachable.** Over Tailscale, confirm both hosts are
   online (`tailscale status`). Over a LAN, confirm you can reach the peer's
   address and that nothing firewalls port `7853`.

4. **macOS Sequoia: the Local Network gate.** Starting in Sequoia, launchd-managed
   daemons get a silent failure when connecting to a LAN address (10.x /
   172.16–31.x / 192.168.x) until a Local Network permission entry exists for
   clipfan. Tailscale (100.x) traffic is unaffected. If your LAN peers don't sync,
   see the
   [Local Network caveat](../README.md#macos-launchd-vs-local-network-privacy) in
   the README for the workaround.

5. **Mixed daemon versions.** clipfan fails closed across versions — upgrade all
   peers together. After a Mac app update, open **Settings… → Fleet**; the app
   flags peers that are older than the bundled daemon and lets you update them in
   place.

6. **Check the `shared_key`.** Every host must carry the *same* `shared_key` in
   `~/.config/clipfan/config.json`. A peer with a different key (or none) can't
   exchange clips. Adding a peer through the app copies the key for you; hosts
   installed by hand need it copied over manually.
