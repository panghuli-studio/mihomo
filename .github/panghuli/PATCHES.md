# Fork patches

Upstream base: `MetaCubeX/mihomo@Alpha`, pinned by `UPSTREAM`.

| Patch | Reason | Reference | Status |
| --- | --- | --- | --- |
| Refuse an empty `dialer-proxy` group | Prevent an implicit `COMPATIBLE` direct connection; defer embedded Tailscale startup until its upstream is ready | [Fork PR #5](https://github.com/panghuli-studio/mihomo/pull/5) | Fork only |
| Unconnected UDP listener destination | Pass a valid unspecified destination from the Tailscale adapter | [Upstream PR #3535](https://github.com/MetaCubeX/mihomo/pull/3535) | Upstream implementation; global fork patch removed |

Alpha packages are marked as prereleases and include the pinned upstream revision in their version. Consumers must pin an explicit version. Synchronize upstream through a merge PR into `dev`, retaining upstream ancestry.
