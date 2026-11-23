# Pay2Own backend

The API and shop admin run as the Fly app `pay2own`.

## Add a hostname

Pointing DNS at Fly is not enough for HTTPS. Fly only presents a certificate for hostnames added on the app. A new name such as `android.pay2own.app` reaches the machine and then fails the TLS handshake until a certificate exists.

1. Read the app addresses:

```sh
fly ips list -a pay2own
```

2. Create two DNS-only records for the hostname. Do not proxy them. As of the current app they are:

| Type | Name | Value |
| --- | --- | --- |
| A | `android` | `66.241.125.82` |
| AAAA | `android` | `2a09:8280:1::206:6f6d:0` |

The IPv4 address is shared. The IPv6 address is dedicated to this app. Run `fly ips list` again before copying these into a new record.

3. Ask Fly to issue the certificate:

```sh
fly certs add android.pay2own.app -a pay2own
fly certs check android.pay2own.app -a pay2own
```

`Status = Issued` means Let's Encrypt finished. `https://android.pay2own.app/health` should then return 200.

`https://android.pay2own.app/` redirects to `https://android.pay2own.app/admin/`. Health checks, the API, and the setup APK stay on their own paths.

`fly certs list -a pay2own` shows every hostname already on the app.
