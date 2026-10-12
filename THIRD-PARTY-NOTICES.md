# Third-Party Notices

3m-ui is licensed under the Eclipse Public License 2.0 (EPL-2.0).

This repository and its release artifacts may include, download, invoke, or distribute third-party software that is subject to its own license terms. A third-party component's license is not changed by the license of 3m-ui.

## Mihomo Core

3m-ui uses the Mihomo Core as a separate core component. The Mihomo Core is distributed under the GNU General Public License, version 3 (GPLv3), according to the license applicable to the Mihomo version included with or downloaded by a particular 3m-ui release.

- Project: https://github.com/MetaCubeX/mihomo
- License: GNU General Public License v3.0
- License text: https://www.gnu.org/licenses/gpl-3.0.html

When redistributing a 3m-ui release together with a Mihomo binary, the Mihomo binary and its corresponding source-code/license obligations remain subject to the GPLv3 terms applicable to that Mihomo release. This notice does not relicense Mihomo under EPL-2.0.

## acme.sh

3m-ui's SSH management menu can drive [acme.sh](https://github.com/acmesh-official/acme.sh) to issue, deploy and renew TLS certificates. acme.sh is **not** bundled with, vendored into, or linked against 3m-ui: it is downloaded and installed as an independent tool on the operator's machine (by default `~/.acme.sh`) and invoked as a separate process by the menu.

- Project: https://github.com/acmesh-official/acme.sh
- License: GNU General Public License v3.0
- License text: https://www.gnu.org/licenses/gpl-3.0.html

Because acme.sh runs as a standalone executable and interacts with 3m-ui only through process invocation and files on disk, it is not a derivative work of 3m-ui. This notice does not relicense acme.sh under EPL-2.0, nor 3m-ui under GPLv3. Where a 3m-ui release or installer gathers acme.sh, that component remains subject to GPLv3 and its license and attribution notices should be retained.

## Other dependencies

Dependencies and bundled components that are not part of 3m-ui itself remain subject to their respective upstream licenses. Where a release includes a third-party component, its applicable license and attribution notices should be retained.
