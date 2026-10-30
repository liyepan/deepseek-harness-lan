# dsh-lan

给**只监听回环地址**的 `dsh web` 加一个局域网入口。

`dsh web` 默认绑定 `127.0.0.1`，并且明确拒绝 `--host 0.0.0.0`（源码里的理由是：浏览器 UI 直通 shell，暴露到网络等于把远程代码执行送出去）。本项目不修改 dsh 的任何配置、不追加任何启动参数，只在旁边加一个网关卡：**dsh 永远只看见 loopback，局域网里的任何一块网卡都能访问。**

```
   手机 / 笔记本（同一个局域网）
   http://172.19.4.240:8080
              │
              │  Host: 172.19.4.240:8080
              │  Origin: http://172.19.4.240:8080
              ▼
        ┌───────────┐
        │  dsh-lan  │   监听 0.0.0.0:8080
        └───────────┘
              │
              │  Host: 127.0.0.1:3080            ← 改写
              │  Origin: http://127.0.0.1:3080   ← 改写
              ▼
        dsh web   127.0.0.1:3080
```

---

## 目录

- [它解决什么](#它解决什么)
- [工作原理](#工作原理)
- [快速开始](#快速开始)
- [命令与参数](#命令与参数)
- [安全模型](#安全模型)
- [常见问题](#常见问题)
- [设计取舍](#设计取舍)
- [开发](#开发)
- [附录：不装网卡的官方做法](#附录不装网卡的官方做法)
- [路线图](#路线图)
- [License](#license)

---

## 它解决什么

`dsh web` 有三道门把局域网挡在外面（以 `@deepseek-ai/dsh` 0.1.5-rc.1 为例）：

| 层 | 位置 | 行为 |
| --- | --- | --- |
| CLI 检查 | `dsh-web-app/lib/startup.js` | `--host 0.0.0.0` 直接报错退出 |
| 绑定 schema | `dsh-host-webserver` 的 Config | `host` 只接受 `127.0.0.1` / `0.0.0.0` |
| Host/Origin 栅栏 | `dsh-client-connection` | `/api` 和 WebSocket 升级要求 Host 是 loopback 或白名单，且 Origin 必须等于 Host |

`dsh-lan` 选择不动 dsh，而是替它对外：

- dsh 的启动命令保持**一字不改**：`dsh web --host 127.0.0.1 --port 3080 --no-open`
- 不写 `~/.dsh` 里的任何配置，不加 `--trusted-host`，不 patch 配置树
- 网关绑 `0.0.0.0`，把 Host / Origin 改写成 dsh 认识的 loopback authority

结果：任意网卡 IP 都能访问，**不需要预先登记任何地址**。

---

## 工作原理

### 1. 静态资源

普通转发，逐字节透传，不缓冲、不重新压缩（`DisableCompression`）。dsh 的 gzip 和 `text/event-stream` 原样到达浏览器。

### 2. `/api` 与 WebSocket

dsh 的栅栏逻辑是：

- `Host` 必须是 loopback，或在 `trustedHosts` 里
- 如果请求带 `Origin`，它的 authority 必须等于 `Host`
- `Sec-Fetch-Site: cross-site` 直接拒

浏览器送来的是 `Host: 172.19.4.240:8080`，dsh 一定不认。网关把这两个头一起改成 `127.0.0.1:3080`，栅栏就过了 —— 认证仍然由 dsh 自己的 cookie 负责，网关不替任何人放行。

WebSocket 的升级请求走同一条 `Rewrite` 路径，所以 `Origin` 也被改写；升级后的字节流由 Go 反代原生双向搬运。

### 3. cookie 为什么不会错位

这是整套方案能成立的关键，值得说清楚：

- dsh 的浏览器 cookie 名是 **Host 值的 SHA-256**，payload 里再用 HMAC 签一次 `authority`
- 因为**每个**请求都被改写成 loopback，dsh 看到的名字和 payload 永远自洽
- 浏览器则是按自己实际访问的 origin（`http://172.19.4.240:8080`）存这个 cookie

两边各看各的，所以同一个 dsh 进程同时服务 `127.0.0.1:3080`、`172.19.4.240:8080`、`10.0.0.5:8080` 都不冲突 —— 换一块网卡、换一个 IP、甚至以后换一个主机名，都不用改任何配置。

### 4. 令牌自动登录

dsh 每次启动生成一个新的 32 字节 token，打印在自己的启动行里。`dsh-lan` 托管启动时解析这一行拿到 token，并把 token 从即将输出到终端的那一行里抹掉 —— 你只会看到：

```
dsh web: http://127.0.0.1:3080/
```

浏览器第一次访问 `/` 时，网关在**转发给上游的那一份请求**里补上 `?token=`，dsh 于是回一个 `303` + `Set-Cookie`，浏览器从此带着 cookie 访问，地址栏始终是干净的 `/`。令牌不打印、不写日志、不进 URL。

需要它的时候（比如手动接管另一个 dsh）：`dsh-lan token` 会把缓存里的令牌打出来。

这个自动补令牌只对**本机网卡地址和 loopback** 生效。这条白名单是安全边界而不是便利：如果对任意 Host 都补，一个 DNS rebinding 页面就能让网关给它发一枚全新的会话 cookie。

---

## 快速开始

要求：编译期 Go 1.24+；运行期不需要安装任何东西（单二进制、零第三方依赖、离线可编译）。`dsh` 由你自己装好。

`dsh` 本身是 Node 程序（启动器是 `#!/usr/bin/env node`），所以**启动 dsh 的那条路径上必须有 node**。本项目不猜、不写死这个路径：需要时用你的**登录 shell**解析一次（`$SHELL -lc "command -v dsh"`），拿到 dsh 和它能用的 `PATH` 再传给子进程。因此版本管理器（nvm 之类）装在哪都无所谓，系统配置里不会出现它的路径。

```bash
git clone https://git.linuxacme.com/liyepan/deepseek-harness-lan.git
cd deepseek-harness-lan
make build          # 生成 ./dsh-lan
./dsh-lan
```

输出大致如下：

```
dsh-lan 0.1.0

  上游      http://127.0.0.1:3080
  监听      0.0.0.0:8080  (所有网卡)
  dsh      /usr/local/bin/dsh web --host 127.0.0.1 --port 3080 --no-open

  局域网地址（任何一块网卡都可用）
    http://172.19.4.240:8080/  (ens33)

  安全提示
    ...
```

手机连同一个 Wi-Fi，直接打开那个地址就能用。首次访问由网关在转发时补上启动令牌，浏览器拿到 cookie 之后就与普通登录无异，地址栏始终是干净的 `/`，令牌不会打印到终端，也不会出现在任何地方。

### 三种使用姿势

| 场景 | 命令 |
| --- | --- |
| 什么都不想管 | `./dsh-lan`（没在跑就拉起 dsh，在跑就挂上去） |
| dsh 已经手动跑着 | `./dsh-lan --attach --token <控制台里那个 token>` |
| 换端口 / 只开某块网卡 | `./dsh-lan --listen 172.19.4.240:9000` |

已经手动跑着 dsh、又不想复制 token 的话，还有一条路：把 dsh 控制台里那条 URL 的 `127.0.0.1` 换成你的局域网 IP 和网关端口，直接粘到浏览器 —— 一次性登录，之后 cookie 自动生效。

### 装成系统服务

四个脚本，都是 root 跑，互不依赖：

```bash
sudo ./install.sh     # 复制二进制到 /usr/bin，生成 /etc/systemd/system/dsh-lan.service
sudo ./enable.sh      # 开机自启 + 立即启动，并回显局域网地址
sudo ./disable.sh     # 停止 + 取消自启（文件都还在）
sudo ./uninstall.sh   # 停服务、删单元、删二进制；加 --purge 连令牌缓存一起删
```

服务以 `sudo` 的调用者身份运行（`User=jarue`），工作目录默认是该用户家目录，这样 dsh 的 `~/.dsh` 和模型看到的工作区都是对的。单元文件里**没有** node 路径、没有 nvm 路径、没有 `PATH` 覆盖：

```ini
[Service]
User=jarue
WorkingDirectory=/home/jarue
Environment=HOME=/home/jarue
StateDirectory=dsh-lan
ExecStart=/usr/bin/dsh-lan --listen 0.0.0.0:8080 --state /var/lib/dsh-lan/state.json
Restart=on-failure
```

需要换用户/端口/工作目录时用环境变量覆盖后重装：

```bash
DSH_LAN_USER=alice DSH_LAN_LISTEN=0.0.0.0:9000 sudo ./install.sh
```

`install.sh` 支持 `DESTDIR=/some/prefix`（不碰系统、不调 systemctl），方便先看一眼生成的文件。

---

## 命令与参数

```
dsh-lan [start] [flags]     启动/接管 dsh，然后对外提供局域网访问
dsh-lan doctor              体检：dsh、端口、网卡、令牌缓存、防火墙提示
dsh-lan token [clear]       查看或清除缓存的启动令牌
dsh-lan version
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--listen` | `0.0.0.0:8080` | 网关绑定地址。`0.0.0.0` = 所有网卡 |
| `--upstream` | `127.0.0.1:3080` | dsh web 的地址 |
| `--dsh` | 自动解析 | dsh 可执行文件；默认用登录 shell 解析（`$SHELL -lc "command -v dsh"`），不写死路径 |
| `--attach` | 关 | 绝不启动 dsh，只接管已在运行的实例 |
| `--token` | 空 | 手动指定启动令牌；空则从 dsh 输出里抓，再退回复用缓存 |
| `--state` | `.dsh-lan/state.json` | 令牌缓存位置（仓库内，已 gitignore） |
| `--allow-host` | 空 | 额外允许自动注入令牌的 Host 名，可重复 |
| `--no-token-inject` | 关 | 关闭自动注入，只用 `?token=` 登录 |
| `--quiet` | 关 | 不打印逐条请求日志 |

令牌缓存只落在仓库目录里，**不写 `~/.dsh`、不写系统配置**。用 `dsh-lan token clear` 随时抹掉。

---

## 安全模型

**能打开这个页面的人，就能在这台机器上以你的身份执行命令、读写文件。** 这不是危言耸听，是 dsh web 的设计目标 —— 它就是给人用的 shell 界面。

因此：

- **只在可信局域网开**。家用路由器、公司内网可以；咖啡馆 Wi-Fi 不行。
- **绝对不要做端口映射**、不要挂在公网 IP 上。要外网访问，套 WireGuard / Tailscale，别裸露 HTTP。
- **没有 TLS**。同一网段上的人可以嗅探到 token 和 cookie。这一点和 dsh 自己提供的传输层一致（dsh 的 cookie 也不带 `Secure`）。
- **没有多用户隔离**。所有连上来的人共用同一个 OS 用户；审批弹窗谁都能点。
- 网关日志里的 `token=` 会被替换成 `***`，但浏览器历史、剪贴板、分享链接不受网关控制。
- 自动注入令牌的 Host 白名单（本机网卡 + loopback）是防 DNS rebinding 的，别为了"方便"关掉；真需要别的名字，用 `--allow-host` 精确加一条。

想更保险：用完 `Ctrl+C` 关掉；或者用完就把 dsh 也停了。

---

## 常见问题

**页面 403**

说明请求到了 dsh 的栅栏上，但 Host/Origin 没被改写。多半是你直连了 `3080`，或者中间还有别的代理。检查是不是打开了 `http://<ip>:3080` 而不是网关端口。

**页面 401**

栅栏过了，但没认证。原因通常是：你用了 `--no-token-inject`；或者访问的 Host 不在白名单（比如某个自定义域名）；或者缓存里的 token 是上一次 dsh 进程的（每次重启都会换）。用带 `?token=` 的链接进去一次即可。

**手机打不开，电脑能开**

按顺序排查：网关是不是绑在 `0.0.0.0` 而不是 `127.0.0.1`（看 banner）；防火墙有没有放行（`dsh-lan doctor` 会提示，例如 `sudo ufw allow 8080/tcp`）；路由器是不是开了 AP 隔离（访客网络常见）；手机和电脑是不是真在同一个网段。

**8080 被占用**

`--listen 0.0.0.0:9000` 换一个。

**dsh 挂了会怎样**

网关会跟着退出，并打印 dsh 最后的输出。前台运行的话直接重跑 `./dsh-lan` 就行。

**能把它丢到后台吗**

`nohup ./dsh-lan --quiet >>dsh-lan.log 2>&1 &`。想开机自启就自己写个 systemd user unit 指向这个二进制（本项目不替你改系统）。

---

## 设计取舍

**为什么用 Go**

标准库的 `httputil.ReverseProxy` 已经处理好流式转发、`FlushInterval`、协议升级和 hop-by-hop 头，WebSocket 不需要额外库；编译产物是单文件，运行期零依赖、不挑 Node 版本、不和 dsh 的 node_modules 打架。整个项目只用标准库，`go build` 离线可完成。

**为什么改写 Host，而不是给 dsh 加信任**

给 dsh 加 `--trusted-host` 也能过栅栏，但那就得把 IP 一个个登记进去，DHCP 一变、换个网段、用主机名访问就得重来。改写 Host 之后 dsh 眼里永远只有 loopback，客户端从哪个地址来都无所谓 —— 这才是"允许局域网访问"，而不是"允许某个 IP 访问"。

**为什么不改 dsh 的配置**

dsh 是用户自己装的。改 `~/.dsh` 意味着升级、重装、换机器都要重来一遍，而且一旦把 dsh 绑到 `0.0.0.0`，暴露面就是整台机器的所有网卡（包括 VPN、docker0、公网网卡）。网关的好处是暴露面只有你主动绑的那一个监听。

**为什么密码门没做**

dsh 自己的 token 已经是 32 字节随机串，再加一层密码在没有 TLS 的前提下收益有限、复杂度不小。要做也应该和 TLS 一起做，见路线图。

---

## 开发

```
cmd/dsh-lan/        入口
internal/cli/       子命令、参数、banner、doctor
internal/gateway/   反向代理：Host/Origin 改写、SSE、WebSocket、日志脱敏
internal/dsh/       dsh 进程托管与启动行解析
internal/lan/       网卡枚举（显示用 / 白名单用两套）
internal/state/     令牌缓存
```

```bash
make test     # go test ./...
make vet
make build
```

测试覆盖了三条最容易错的地方：Host/Origin 改写、令牌只注入可信 Host、升级请求不被 `recorder` 破坏。

### 已在真实 dsh 上验证的行为

对一个正在运行的 `dsh web`（`127.0.0.1:3080`）发起同一组请求：

| 请求 | 直连 3080 | 经网关 |
| --- | --- | --- |
| `/api/*`，Host 为局域网 IP | `403`（栅栏拒绝） | `401`（栅栏通过，等待认证） |
| `/api/remote.mux` WebSocket 升级 | — | `401`（不是 403） |
| `/` 无 cookie | `401` | `401` |

401 而不是 403，正是栅栏被正确改写后应有的结果。

---

## 附录：不装网关的官方做法

如果你能接受"绑所有网卡 + 没有额外控制"，dsh 自己的代码路径其实已经准备好，只需在 `~/.dsh/profiles/web/cordis.patch.yml` 里覆盖 `webserver` 那一行的 `host`（patch 会整段替换 `config`，所以其余键要一起重写）：

```yaml
- id: webserver
  config:
    host: '0.0.0.0'
    port: !!js ctx.webStartup.port ?? 3080
    compression: gzip
    compressionLevel: 1
    compressionThresholdBytes: 1024
```

之后 `dsh web` 会自己打印 `(LAN: http://<ip>:3080/?token=...)`。这条路不需要本项目，代价是：暴露所有网卡、只能用 IP 字面量访问（不认主机名、不认 IPv6）、没有请求日志、没有令牌注入。本项目没有采用它，原因见上面的设计取舍。

---

## 路线图

- [ ] 可选 TLS（自签证书，解决同网段嗅探和 `navigator.clipboard` 之类需要安全上下文的 API）
- [ ] 终端二维码，手机扫码直连
- [ ] 可选口令门（与 TLS 一起做才有意义）
- [ ] mDNS 广播，让手机用 `hostname.local` 访问
- [ ] systemd user unit 模板

---

## License

MIT
