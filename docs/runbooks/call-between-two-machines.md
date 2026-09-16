# Runbook: A Call Between Two Machines

**Use this to hold a real call between your development machine and somebody else's, on another network.** It is a development setup for trying Convia the way people will use it, not a way to run it for anybody else.

**These steps have not been executed yet.** Each one follows from how Convia and LiveKit are configured, and the places most likely to need adjusting are marked. Record here what actually happened the first time it is run.

## What this can and cannot do

Your friend joins **your** installation: they open your Convia in their browser, create an account on it, and you invite them into a room. A call between two installations — each of you running your own Convia — is `M33-002` and does not exist yet.

Three things stand between a friend and your call today:

1. **Convia has to be served over HTTPS.** The session cookie is `__Host-convia_session`, which a browser only keeps over HTTPS or on `localhost`. Over plain HTTP your friend signs in and is immediately signed out.
2. **LiveKit has to be reachable.** The development `docker-compose.yml` binds it to `127.0.0.1` and announces `--node-ip 127.0.0.1`, which on your friend's machine is their own computer. And a page served over HTTPS cannot open a `ws://` connection, so the signal has to be `wss://`.
3. **There has to be a network path between you** that carries UDP, which is what the media travels on. An HTTP tunnel (Cloudflare Tunnel, ngrok) carries the page but not the media.

[Tailscale](https://tailscale.com) answers all three: both machines join one private network, `tailscale serve` gives your Convia and your LiveKit HTTPS names with real certificates, and the media travels directly between the two Tailscale addresses.

## Before you start

- Tailscale installed on both machines, and your friend in your tailnet: invite them from the admin console, or share your machine with them.
- **HTTPS certificates enabled** for the tailnet (admin console → DNS → HTTPS Certificates). Your machine then has a name like `your-machine.tail1234.ts.net`.
- Your machine's Tailscale address: `tailscale ip -4`, something like `100.101.102.103`.
- Convia built with the interface (`npm run build --prefix web`, then `go build ./cmd/convia`).

Below, `NAME` is your machine's `ts.net` name and `ADDRESS` is its Tailscale address.

## 1. Make LiveKit reachable

Create `docker-compose.override.yml` beside `docker-compose.yml`. It is a local file: **do not commit it.**

```yaml
services:
  livekit:
    command: --dev --bind 0.0.0.0 --node-ip ADDRESS
    ports:
      - "ADDRESS:7881:7881"
      - "ADDRESS:7882:7882/udp"
```

`--node-ip` is the address LiveKit tells each browser to send media to, so it has to be one your friend can reach. The two ports are LiveKit's TCP fallback and its UDP media port, now also published on the Tailscale address.

Then start it, and allow the ports through the firewall on the Tailscale interface if yours asks:

```bash
docker compose --profile media up -d postgres livekit
```

## 2. Give Convia and LiveKit HTTPS names

```bash
tailscale serve --bg --https=443 http://127.0.0.1:8080
tailscale serve --bg --https=8443 http://127.0.0.1:7880
```

The first serves Convia at `https://NAME`, the second LiveKit's signal at `wss://NAME:8443`. `tailscale serve status` lists both.

## 3. Start Convia

Use your development environment, with three additions:

```bash
export CONVIA_LIVEKIT_CLIENT_URL=wss://NAME:8443
export CONVIA_TRUSTED_PROXIES=127.0.0.1/32
export CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES=true
./convia serve
```

- `CONVIA_LIVEKIT_CLIENT_URL` is the address a browser is given for the call, and the one the page's Content-Security-Policy allows. Convia itself still reaches LiveKit at `CONVIA_LIVEKIT_URL`, on loopback.
- `CONVIA_TRUSTED_PROXIES` lets Convia see each browser's own address behind `tailscale serve`, so the budgets for failed sign-ins are per person rather than shared by everybody coming through the proxy.
- `CONVIA_PEERS_ALLOW_PRIVATE_ADDRESSES` lets an invitation link that names your machine be followed. Tailscale addresses are in a range Convia treats as private.

**Check this first:** open `https://NAME` yourself and sign in. Convia refuses a state-changing request whose `Origin` is not the address it was reached at, built from the `Host` header and `X-Forwarded-Proto`. If signing in fails and Convia's log says `a state-changing request was refused on its origin` with reason `mismatch`, `tailscale serve` is not passing the `Host` your browser used, and that is the step to fix before going on.

## 4. Invite your friend and call

1. **Open Convia at `https://NAME`, not at `localhost`.** An invitation link names the address the page was opened at, and a `localhost` link sends your friend to their own computer.
2. Your friend opens `https://NAME`, creates an account, and sends you their handle from **Settings → Account**.
3. Open a room, and from **People → Invite by handle** make a link for that handle. Send it to your friend.
4. Your friend chooses **Join with a link**, pastes it, and joins.
5. Either of you presses **Start call**, and the other **Join call**.

## If the call does not connect

| What you see | Where to look |
| --- | --- |
| Your friend is signed out right after signing in | They are on `http://`, not `https://NAME`. |
| Signing in fails for everybody on `https://NAME` | The `Origin` check, in step 3. |
| The call starts and nobody hears anybody | The media path: `--node-ip` is not `ADDRESS`, or UDP 7882 is blocked. LiveKit falls back to TCP 7881 when UDP is blocked. `docker logs convia-livekit` shows the candidates it offered. |
| The browser console refuses a connection to `wss://NAME:8443` | `CONVIA_LIVEKIT_CLIENT_URL` is unset or different, so the page's policy does not allow it. |
| The call never ends after someone closes the page | LiveKit cannot reach Convia's `/media/reports`; the webhook in `docker-compose.yml` must still point at your machine. |

## Undo it

```bash
tailscale serve reset
docker compose --profile media stop livekit
rm docker-compose.override.yml
```

The accounts and rooms your friend made stay in your development database.
