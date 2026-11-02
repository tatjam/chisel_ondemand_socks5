This is meant to be used with chisel running in some VPS or similar, see [my personal website code](https://github.com/tatjam/station) for an example that launches chisel only when needed on the backend.

Because it keeps the connection started only when used, it can be used on lazy, free VPSs which only run your binary on HTTP request (and without using excessive resources on their end either! win-win), so this can serve as a "extremely poor man's" VPN.
