# pocketchip-apt-rescue

`pocketchip-apt-rescue` is a small APT rescue proxy for the discontinued **Next Thing Co. PocketC.H.I.P.** and **C.H.I.P.**

It helps old PocketCHIP Debian/Jessie systems run `apt-get update` again without first replacing every broken APT source by hand.

The PocketCHIP connects to this proxy using plain HTTP. The proxy runs on a modern computer, rewrites known dead repository URLs, fetches working upstream URLs over HTTPS, and sends the result back to the PocketCHIP over plain HTTP.

```text
PocketCHIP apt-get
  -> plain HTTP request
  -> pocketchip-apt-rescue on a modern computer
  -> HTTPS request to archive/mirror
  -> plain HTTP response back to PocketCHIP
```

## Why you need this

A freshly restored or long-unupdated PocketCHIP commonly has old APT sources that no longer work.

Typical failures include:

```text
E: The method driver /usr/lib/apt/methods/https could not be found
404 Not Found
502 Bad Gateway
Failed to fetch http://opensource.nextthing.co/...
Failed to fetch http://security.debian.org/dists/jessie/updates/...
```

Common causes:

* the original Next Thing Co. package repositories are gone;
* old Debian Jessie mirrors moved to archive locations;
* old HTTP repositories may redirect to HTTPS;
* old PocketCHIP APT may not have `apt-transport-https`;
* old TLS/certificate support may be broken;
* stock source lists may still reference `http.debian.net`, `security.debian.org`, `jessie-backports`, or `opensource.nextthing.co`.

This proxy works around those problems from a modern computer.

## What it rewrites

The proxy rewrites common old PocketCHIP and Debian Jessie repository URLs automatically.

| Old request from PocketCHIP                                 | Upstream request made by proxy                                        |
| ----------------------------------------------------------- | --------------------------------------------------------------------- |
| `http://http.debian.net/debian/...`                         | `https://archive.debian.org/debian/...`                               |
| `http://deb.debian.org/debian/...`                          | `https://archive.debian.org/debian/...`                               |
| `http://ftp.debian.org/debian/...`                          | `https://archive.debian.org/debian/...`                               |
| `http://security.debian.org/dists/jessie/updates/...`       | `https://archive.debian.org/debian-security/dists/jessie/updates/...` |
| `http://opensource.nextthing.co/chip/debian/repo/...`       | `https://chip.jfpossibilities.com/chip/debian/repo/...`               |
| `http://opensource.nextthing.co/chip/debian/pocketchip/...` | `https://chip.jfpossibilities.com/chip/debian/pocketchip/...`         |
| `http://opensource.nextthing.co/dists/jessie/...`           | `https://chip.jfpossibilities.com/chip/debian/repo/dists/jessie/...`  |

The PocketCHIP still only needs plain HTTP access to the proxy.

## What it does not do

This tool does not:

* flash PocketCHIP firmware;
* replace the PocketCHIP kernel;
* provide a modern Linux distribution;
* guarantee that every old package still exists;
* guarantee that Debian upgrades preserve the PocketCHIP UI;
* fix arbitrary third-party APT repositories.

It is a bootstrap tool for restoring package access.

## Security warning

Do not expose this proxy to the public internet.

Run it only on a trusted local network. Ideally, bind it to a local interface or firewall it so only your PocketCHIP can connect.

## Build

On the modern computer:

```bash
git clone https://github.com/arran4/pocketchip-apt-rescue.git
cd pocketchip-apt-rescue
go build -o pocketchip-apt-rescue .
```

## Run

Start the proxy:

```bash
./pocketchip-apt-rescue
```

By default it listens on:

```text
:3142
```

For more logging:

```bash
POCKETCHIP_PROXY_LOG_LEVEL=debug ./pocketchip-apt-rescue
```

For full request header dumps:

```bash
POCKETCHIP_PROXY_LOG_LEVEL=debug \
POCKETCHIP_PROXY_DUMP_REQUESTS=true \
./pocketchip-apt-rescue
```

## Configure the PocketCHIP

Find the IP address of the modern computer running the proxy.

On Linux:

```bash
ip addr
```

Assume the proxy computer is:

```text
192.168.1.50
```

On the PocketCHIP, create `/etc/apt/apt.conf.d/01proxy`:

```text
Acquire::http::Proxy "http://192.168.1.50:3142";
Acquire::https::Proxy "false";
```

Or use the `apt-proxy.conf` file from this repository:

```bash
curl -fsSL https://raw.githubusercontent.com/arran4/pocketchip-apt-rescue/main/apt-proxy.conf \
  | sed 's/YOUR_DESKTOP_IP/192.168.1.50/g' \
  | sudo tee /etc/apt/apt.conf.d/01proxy
```

Replace `192.168.1.50` with the IP address of the computer running the proxy.

## Add Debian archive compatibility settings

Old Debian archive metadata is expired. Most PocketCHIP systems need this setting while using Jessie archive repositories:

```text
Acquire::Check-Valid-Until "false";
```

Install it from this repository:

```bash
curl -fsSL https://raw.githubusercontent.com/arran4/pocketchip-apt-rescue/main/apt-archive.conf \
  | sudo tee /etc/apt/apt.conf.d/99pocketchip-archive
```

If `curl` is not installed, use `wget`:

```bash
wget -O - https://raw.githubusercontent.com/arran4/pocketchip-apt-rescue/main/apt-archive.conf \
  | sudo tee /etc/apt/apt.conf.d/99pocketchip-archive
```

Then retry:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
```

### If APT still complains about unsigned or untrusted repositories

Some restored PocketCHIP images may also need temporary insecure archive options.

Install the fallback config:

```bash
curl -fsSL https://raw.githubusercontent.com/arran4/pocketchip-apt-rescue/main/apt-insecure.conf \
  | sudo tee /etc/apt/apt.conf.d/99pocketchip-insecure
```

Or with `wget`:

```bash
wget -O - https://raw.githubusercontent.com/arran4/pocketchip-apt-rescue/main/apt-insecure.conf \
  | sudo tee /etc/apt/apt.conf.d/99pocketchip-insecure
```

Then retry:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
```

The insecure config should be treated as a bootstrap workaround, not a permanent setting.

## Run the first update

Leave the existing PocketCHIP source lists alone for the first attempt.

Run:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
```

Watch the proxy logs on the modern computer.

A successful Debian security rewrite looks like:

```text
rewrite rule=debian-security-to-archive before=http://security.debian.org/dists/jessie/updates/main/binary-armhf/Packages after=https://archive.debian.org/debian-security/dists/jessie/updates/main/binary-armhf/Packages
request remote=192.168.1.23:42100 method=GET original=http://security.debian.org/dists/jessie/updates/main/binary-armhf/Packages upstream=https://archive.debian.org/debian-security/dists/jessie/updates/main/binary-armhf/Packages
response status=200
```

A successful PocketCHIP repository rewrite looks like:

```text
rewrite rule=nextthing-to-jfpossibilities before=http://opensource.nextthing.co/chip/debian/pocketchip/dists/jessie/main/binary-armhf/Packages after=https://chip.jfpossibilities.com/chip/debian/pocketchip/dists/jessie/main/binary-armhf/Packages
request remote=192.168.1.23:42102 method=GET original=http://opensource.nextthing.co/chip/debian/pocketchip/dists/jessie/main/binary-armhf/Packages upstream=https://chip.jfpossibilities.com/chip/debian/pocketchip/dists/jessie/main/binary-armhf/Packages
response status=200
```

If `apt-get update` works, install basic HTTPS/certificate support:

```bash
sudo apt-get install ca-certificates apt-transport-https
```

## Test the proxy from the modern computer

Test Debian archive rewriting:

```bash
curl -v -x http://127.0.0.1:3142 \
  http://http.debian.net/debian/dists/jessie/Release \
  -o /tmp/jessie-release
```

Test Debian security rewriting:

```bash
curl -v -x http://127.0.0.1:3142 \
  http://security.debian.org/dists/jessie/updates/main/binary-armhf/Packages \
  -o /tmp/jessie-security-packages
```

Test old Next Thing Co. rewriting:

```bash
curl -v -x http://127.0.0.1:3142 \
  http://opensource.nextthing.co/chip/debian/pocketchip/dists/jessie/Release \
  -o /tmp/pocketchip-release
```

Each test should produce a `rewrite rule=...` line in the proxy logs.

## Environment variables

| Variable                               |                     Default | Purpose                                     |
| -------------------------------------- | --------------------------: | ------------------------------------------- |
| `POCKETCHIP_PROXY_LISTEN`              |                     `:3142` | Listen address                              |
| `POCKETCHIP_PROXY_UPSTREAM`            |                       empty | Optional upstream proxy                     |
| `POCKETCHIP_PROXY_TIMEOUT`             |                      `120s` | Network timeout                             |
| `POCKETCHIP_PROXY_LOG_LEVEL`           |                      `info` | `quiet`, `info`, or `debug`                 |
| `POCKETCHIP_PROXY_DUMP_REQUESTS`       |                     `false` | Dump outbound request headers               |
| `POCKETCHIP_PROXY_INSECURE_TLS`        |                     `false` | Disable upstream TLS verification           |
| `POCKETCHIP_PROXY_ALLOW_CONNECT`       |                     `false` | Allow CONNECT tunnelling                    |
| `POCKETCHIP_PROXY_HTTPS_UPSTREAM`      |                      `true` | Fetch upstream over HTTPS                   |
| `POCKETCHIP_PROXY_REWRITE_KNOWN_REPOS` |                      `true` | Rewrite known old PocketCHIP/Jessie URLs    |
| `POCKETCHIP_PROXY_BLOCK_DEAD_REPOS`    |                     `false` | Return explicit errors for known dead repos |
| `POCKETCHIP_PROXY_USER_AGENT`          | `pocketchip-apt-rescue/0.1` | Upstream User-Agent                         |

Example:

```bash
POCKETCHIP_PROXY_LISTEN="192.168.1.50:3142" \
POCKETCHIP_PROXY_LOG_LEVEL=debug \
./pocketchip-apt-rescue
```

## Troubleshooting

### `apt-get update` does not reach the proxy

Check the proxy config on the PocketCHIP:

```bash
cat /etc/apt/apt.conf.d/01proxy
```

It should contain:

```text
Acquire::http::Proxy "http://192.168.1.50:3142";
Acquire::https::Proxy "false";
```

Check that the PocketCHIP can reach the proxy computer:

```bash
ping 192.168.1.50
```

Check that the proxy is listening on the modern computer:

```bash
ss -ltnp | grep 3142
```

### The proxy logs show `404`

A `404` means the proxy contacted an upstream server, but the requested file does not exist at the rewritten location.

Inspect the active source entries on the PocketCHIP:

```bash
grep -R "^[[:space:]]*deb " /etc/apt/sources.list /etc/apt/sources.list.d/*.list 2>/dev/null
```

If the source is unusual or heavily edited, clean it up manually.

### Fallback source list

Only use this if the automatic rewrite approach does not work.

A conservative Jessie fallback is:

```text
deb http://archive.debian.org/debian/ jessie main contrib non-free
deb http://archive.debian.org/debian-security/ jessie/updates main contrib non-free
deb http://chip.jfpossibilities.com/chip/debian/repo jessie main
deb http://chip.jfpossibilities.com/chip/debian/pocketchip jessie main
```

To replace the source list:

```bash
sudo cp /etc/apt/sources.list /etc/apt/sources.list.bak

sudo sh -c 'cat > /etc/apt/sources.list <<EOF
deb http://archive.debian.org/debian/ jessie main contrib non-free
deb http://archive.debian.org/debian-security/ jessie/updates main contrib non-free
deb http://chip.jfpossibilities.com/chip/debian/repo jessie main
deb http://chip.jfpossibilities.com/chip/debian/pocketchip jessie main
EOF'
```

Then retry:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
```

### `The method driver /usr/lib/apt/methods/https could not be found`

The PocketCHIP is trying to use HTTPS directly.

Make sure your PocketCHIP APT sources are HTTP URLs, and make sure `/etc/apt/apt.conf.d/01proxy` contains:

```text
Acquire::https::Proxy "false";
```

The proxy fetches HTTPS upstream. The PocketCHIP should not need to.

### `Check-Valid-Until` or expired repository errors

Add the archive compatibility config:

```bash
sudo sh -c 'cat > /etc/apt/apt.conf.d/99archive <<EOF
Acquire::Check-Valid-Until "false";
Acquire::AllowInsecureRepositories "true";
Acquire::AllowDowngradeToInsecureRepositories "true";
APT::Get::AllowUnauthenticated "true";
EOF'
```

## Upgrading Debian

Upgrade slowly.

Suggested path for a PocketCHIP handheld:

```text
Jessie -> Stretch -> Buster -> Bullseye
```

Avoid jumping directly from Jessie to a current Debian release.

The PocketCHIP uses old vendor kernel and device-specific packages. A newer userspace may work, but display, keyboard, Wi-Fi, battery, charging, and the PocketCHIP UI can break.

After each upgrade step, check:

* boot;
* screen;
* keyboard;
* Wi-Fi;
* charging/battery status;
* PocketCHIP UI;
* SSH access, if enabled.

## After APT starts working

Once `apt-get update` works through the proxy, the next step is to make the system less dependent on rescue settings.

Start by installing basic trust and HTTPS packages:

```bash
sudo apt-get --allow-unauthenticated install debian-archive-keyring ca-certificates apt-transport-https
```

If your old `apt-get` does not support `--allow-unauthenticated`, use:

```bash
sudo apt-get -o APT::Get::AllowUnauthenticated=true install debian-archive-keyring ca-certificates apt-transport-https
```

Then retry:

```bash
sudo apt-get update
echo $?
```

If the exit code is `0`, APT is usable. It may still show warnings while using old archived repositories.

## Clean up APT sources

The proxy is intended to get old stock PocketCHIP sources working without editing them first. After the first successful update, clean up the active source lists so future updates are more predictable.

Inspect active sources:

```bash
grep -R "^[[:space:]]*deb " /etc/apt/sources.list /etc/apt/sources.list.d/*.list 2>/dev/null
```

Back up the existing source list:

```bash
sudo cp /etc/apt/sources.list /etc/apt/sources.list.bak
sudo mkdir -p /root/apt-sources-backup
sudo cp -a /etc/apt/sources.list.d /root/apt-sources-backup/
```

Remove or replace obsolete entries such as:

```text
opensource.nextthing.co
http.debian.net
security.debian.org jessie/updates
ftp.us.debian.org
jessie-backports
jessie-updates
```

For a cleaned-up Jessie/PocketCHIP setup, use:

```text
deb http://archive.debian.org/debian/ jessie main contrib non-free
deb http://archive.debian.org/debian-security/ jessie/updates main contrib non-free
deb http://chip.jfpossibilities.com/chip/debian/repo jessie main
deb http://chip.jfpossibilities.com/chip/debian/pocketchip jessie main
```

To replace `/etc/apt/sources.list` with that fallback:

```bash
sudo sh -c 'cat > /etc/apt/sources.list <<EOF
deb http://archive.debian.org/debian/ jessie main contrib non-free
deb http://archive.debian.org/debian-security/ jessie/updates main contrib non-free
deb http://chip.jfpossibilities.com/chip/debian/repo jessie main
deb http://chip.jfpossibilities.com/chip/debian/pocketchip jessie main
EOF'
```

Then update again:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
```

## Test whether the proxy is still needed

After APT works and sources are cleaned up, temporarily disable the proxy:

```bash
sudo mv /etc/apt/apt.conf.d/01proxy /etc/apt/apt.conf.d/01proxy.disabled
```

Then test direct APT:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
echo $?
```

If the exit code is `0`, the proxy is no longer needed for the current source list.

Remove the disabled proxy config:

```bash
sudo rm -f /etc/apt/apt.conf.d/01proxy.disabled
```

If direct APT fails, restore the proxy:

```bash
sudo mv /etc/apt/apt.conf.d/01proxy.disabled /etc/apt/apt.conf.d/01proxy
```

Keep the proxy until direct `apt-get update` works or until you have upgraded to a suite/source configuration that no longer needs URL rewriting.

## Remove temporary insecure settings

Remove the insecure fallback as soon as package authentication is working:

```bash
sudo rm -f /etc/apt/apt.conf.d/99pocketchip-insecure
```

Then test:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
echo $?
```

If APT fails only because of old signature or key errors, restore the insecure config temporarily and continue bootstrapping.

Do not leave `99pocketchip-insecure` enabled permanently.

## Remove archive expiry override

Keep the archive expiry override while using old archived Debian releases such as Jessie:

```text
/etc/apt/apt.conf.d/99pocketchip-archive
```

or:

```text
/etc/apt/apt.conf.d/99archive
```

After upgrading away from old archived releases, remove it:

```bash
sudo rm -f /etc/apt/apt.conf.d/99archive
sudo rm -f /etc/apt/apt.conf.d/99pocketchip-archive
```

Then test:

```bash
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
sudo apt-get update
echo $?
```

## Distro-upgrade source changes

If upgrading Debian, update source lists one release at a time.

Recommended order:

```text
Jessie -> Stretch -> Buster -> Bullseye
```

Do not jump directly from Jessie to a current Debian release.

At each step:

1. Back up `/etc/apt/sources.list`.
2. Replace only the Debian suite name and matching security/archive entries.
3. Run `apt-get update`.
4. Run `apt-get upgrade`.
5. Run `apt-get dist-upgrade`.
6. Reboot if needed.
7. Check PocketCHIP-specific hardware and UI before continuing.

Example upgrade flow:

```bash
sudo cp /etc/apt/sources.list /etc/apt/sources.list.before-upgrade
sudo apt-get update
sudo apt-get upgrade
sudo apt-get dist-upgrade
```

After each release upgrade, check:

* boot;
* screen;
* keyboard;
* Wi-Fi;
* charging/battery status;
* PocketCHIP UI;
* SSH access, if enabled.

The CHIP/PocketCHIP-specific repositories may not have packages for newer Debian suites. Keep PocketCHIP-specific source entries pinned to known working mirrors unless you know the replacement suite exists.

If a distro upgrade removes or breaks PocketCHIP-specific packages, stop and fix that release before continuing.

## Related resources

* C.H.I.P. Flash Collection: https://archive.org/details/C.h.i.p.FlashCollection
* JF Possibilities CHIP mirror: https://chip.jfpossibilities.com/
* r/ChipCommunity: https://www.reddit.com/r/ChipCommunity/
* fixchip: https://github.com/daisyUniverse/fixchip
* PocketCHIP flash utils: https://github.com/SaltyCybernaut/PocketCHIP-flash-utils

## Disclaimer

PocketCHIP and C.H.I.P. are discontinued devices. Old Debian releases and old NTC repositories are unsupported. Use this tool at your own risk and expect some upgrade paths to break.
