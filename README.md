This is meant to be used with [chisel](https://github.com/jpillora/chisel) running in some VPS or similar, see [my personal website code](https://github.com/tatjam/station) for an example that launches chisel only when needed on the backend.

Because it keeps the connection started only when used, it can be used on lazy, free VPSs which only run your binary on HTTP request (and without using excessive resources on their end either! win-win), so this can serve as a "extremely poor man's" VPN.

# Usage

I use it via systemd. Just place the binary after `go build` somewhere reasonable, create a .env:

```env
CHISEL_USER=user
CHISEL_PASS=password
```

and a systemd unit file (`~/.config/systemd/user/chisel-ondemand.service`)

```systemd
[Unit]
Description=On-demand SOCKS proxy via chisel

[Service]
EnvironmentFile=%h/.config/chisel-ondemand.env
ExecStart=%h/.local/bin/chisel_ondemand_socks5 https://your.tunnel.com/wathever
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

and enable it

```
systemctl --user daemon-reload
systemctl --user enable --now chisel-ondemand
```

Obviously for this to work, the URL needs to have chisel running (or start running after it receives a HTTP request!) with the matching user and "socks" access for said user.
