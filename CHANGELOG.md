# Changelog

## [0.5.0](https://github.com/akbxr/herdsman/compare/v0.4.0...v0.5.0) (2026-11-08)


### Features

* group the herd's agents and shells apart ([6589a2d](https://github.com/akbxr/herdsman/commit/6589a2d79c94537de444a09c342f8a5fbca55ed8))
* reach the Mac through a Cloudflare tunnel, and every route it has ([d012244](https://github.com/akbxr/herdsman/commit/d012244fa9421ec12b9cf374c404a9266ce49e9c))
* show the agent's todo list, subagents and background tasks ([a09dfda](https://github.com/akbxr/herdsman/commit/a09dfda46dd0794e99c7c58f971ef1e89697ac32))


### Bug Fixes

* hide the todo bar once all the work is done ([5032bad](https://github.com/akbxr/herdsman/commit/5032bad18c06bd89be6ebd7784f090d10f56aaab))

## [0.4.0](https://github.com/akbxr/herdsman/compare/v0.3.1...v0.4.0) (2026-11-04)


### Features

* pin agents and tabs to the top of the herd ([3181b36](https://github.com/akbxr/herdsman/commit/3181b36c290e7e6cb2be9a17861dbfe73973703e))

## [0.3.1](https://github.com/akbxr/herdsman/compare/v0.3.0...v0.3.1) (2026-11-02)


### Bug Fixes

* chat of a transcript longer than the most read never loaded ([8a4bd99](https://github.com/akbxr/herdsman/commit/8a4bd99d7c036b027c68fa9b3f1707fb9f55012e))

## [0.3.0](https://github.com/akbxr/herdsman/compare/v0.2.0...v0.3.0) (2026-11-02)


### Features

* session titles and search on the herd ([a1d84e7](https://github.com/akbxr/herdsman/commit/a1d84e773d14a526a931c9fbc1c33c9af97b410e))

## [0.2.0](https://github.com/akbxr/herdsman/compare/v0.1.0...v0.2.0) (2026-11-01)


### ⚠ BREAKING CHANGES

* QR codes and links use herdsman://, the Android app has a new id and must be paired again

### Features

* a live terminal on the phone instead of a text dump ([2d17818](https://github.com/akbxr/herdsman/commit/2d17818cb93d6c2f3e14abb7c33e0da157cae58f))
* **android:** drive herdr CLI panes beside the Herdr app ([6320c50](https://github.com/akbxr/herdsman/commit/6320c5041eb44270bcca45c391c07e24c9efdc60))
* **android:** smooth terminal scrolling with momentum ([56a8c89](https://github.com/akbxr/herdsman/commit/56a8c896163093954bd87bee06fd753c5cb1014d))
* **android:** terminals on other machines ([7bc1c96](https://github.com/akbxr/herdsman/commit/7bc1c9661f547c4460b152d4d450f18316935f1c))
* answer from notifications, select and zoom the terminal, keep the Mac awake ([cd8af92](https://github.com/akbxr/herdsman/commit/cd8af92a1797ab0ad3581235bc0563964572b27a))
* chat history, search and pictures ([efeac64](https://github.com/akbxr/herdsman/commit/efeac647f2fbc57e3be5d8fbb2348f9d8f8b5349))
* chat view and message box suggestions ([601f5bf](https://github.com/akbxr/herdsman/commit/601f5bfea62b3ddd1c0b288d164a906b3c18dbee))
* commit and discard from the Changes tab ([67c84e5](https://github.com/akbxr/herdsman/commit/67c84e539e96be86c7aad0ae8c01ac12c485ed23))
* control Herdr on the Mac from an Android phone ([6b5ec71](https://github.com/akbxr/herdsman/commit/6b5ec71abf31af5691575118b94b92672d5c3c27))
* control Herdr panes and tabs, attach files, and keep the terminal in place ([c88e4e2](https://github.com/akbxr/herdsman/commit/c88e4e27fd573dfea746c0c880086c03afc59991))
* live screen in the chat while the agent works ([6da72c0](https://github.com/akbxr/herdsman/commit/6da72c0804d858cecb24a05a686d13f0b17c822f))
* notifications when an agent needs you, finishes or fails ([91b6b13](https://github.com/akbxr/herdsman/commit/91b6b13e6e281c629a212b6fc8ff12dce9df3349))
* pick the model and thinking level from the app ([c8191da](https://github.com/akbxr/herdsman/commit/c8191da4c78f15bd80373bd972d21d86b443c756))
* prompt choices, live chat, changes view and shimmer ([b66703f](https://github.com/akbxr/herdsman/commit/b66703f4e19cd003716dd1343d019e9ee9321ca9))
* resume an earlier session from the phone ([ee31ada](https://github.com/akbxr/herdsman/commit/ee31adae65b6e42e6fd92098ded110047fb870b5))
* resume earlier sessions in the herdr CLI ([3ef3447](https://github.com/akbxr/herdsman/commit/3ef344750ad9698183a0522c100b6669172367bf))
* **server:** drive herdr CLI panes beside the Herdr app ([f717a78](https://github.com/akbxr/herdsman/commit/f717a7873df2c1c63f73a265d5cb1be8cfdd741a))
* **server:** install as a login service, and find the real Tailscale address ([75f8da7](https://github.com/akbxr/herdsman/commit/75f8da78ff607d67ecb6e83536e6eed455ea40f7))
* **server:** run tasks and read terminals on other machines ([d4bfdf0](https://github.com/akbxr/herdsman/commit/d4bfdf0d171934ddc64d2d24e6bb7550f16c0159))
* start a task from the phone ([729c487](https://github.com/akbxr/herdsman/commit/729c487852ff2bac34f6502beb9ffbe0e6eafeac))


### Bug Fixes

* a chat is only ever its own session's ([f5e34b8](https://github.com/akbxr/herdsman/commit/f5e34b8ecc9003f44e67586afd57205595adce84))
* **android:** changes kept on unchanged polls, tabs of every space, Markdown tables ([4ea811c](https://github.com/akbxr/herdsman/commit/4ea811cb4372a075fa98b97e4dd6b88056a5b636))
* **android:** hide the notifications card once they are on ([b18c6ec](https://github.com/akbxr/herdsman/commit/b18c6ec33cfbc83be8e8c18a657be907e777ea98))
* **android:** pair on the sixth digit and stop polling in the background ([3f78338](https://github.com/akbxr/herdsman/commit/3f7833887e7f28d99323d429b433eab7871487a2))
* **android:** split a herdr pane with target_pane_id ([0532ca2](https://github.com/akbxr/herdsman/commit/0532ca23c3e3baf57c16434ad000a108a97b50cc))
* **android:** terminal stuck on connecting ([323beca](https://github.com/akbxr/herdsman/commit/323beca0f46fa2123fba4ce7c78b489a096e894e))
* chat empty under launchd ([ad0e325](https://github.com/akbxr/herdsman/commit/ad0e32524a05d25e0b7f3ec8ed42c161ecd6c87b))
* chat follows the session and the program in the terminal ([9560912](https://github.com/akbxr/herdsman/commit/9560912b907e99d31933efaed04cb4f333bc7065))
* keep the chat across a rewind ([f56143d](https://github.com/akbxr/herdsman/commit/f56143d319c07de02ec34975c859dd07773d7013))
* no blank rows after the keyboard closes, and an exact copy after any resize ([7d3ab56](https://github.com/akbxr/herdsman/commit/7d3ab56654d706492086ae3eeac093b0c522e21b))


### Performance Improvements

* **android:** release build with R8, fewer recompositions while scrolling ([6a0f827](https://github.com/akbxr/herdsman/commit/6a0f82720e30b56ed5d46c554f726ce43b4ffcb6))


### Reverts

* drop the live terminal block from the chat ([d0a0bf6](https://github.com/akbxr/herdsman/commit/d0a0bf6daf0b1ca6123b59340995bd166d75d582))


### Code Refactoring

* rename herdr-remote to herdsman ([0ca781d](https://github.com/akbxr/herdsman/commit/0ca781d69ef4da4e0f225558f83eaa663e6f1c9a))
