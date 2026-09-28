# GitHub 转发代理

在「设置 → GitHub 代理」配置监听端口和出站链路，独立点击「保存 GitHub 代理设置」；然后到「GitHub 代理」页面开启服务。默认端口为 `8719`，默认关闭，开关状态随应用重启保留。该服务与 LLM 网关的设置、启停、调用日志及 Token 统计相互独立。

服务监听本机所有 IPv4 网卡，页面提供回环、网卡与主机名地址及复制按钮。局域网设备使用宿主机的局域网 IP，并确保防火墙允许该端口。关闭后不再监听；再次开启即可接收请求。运行中修改端口会最多等待在途请求 3 秒，再结束剩余连接；新端口无法绑定时保留原服务和配置。仅修改出站配置时，新请求立即采用新配置，在途请求继续完成。

## 地址与用法

将 `https://github.com` 替换为 `http://127.0.0.1:8719/github`（远程客户端需替换主机地址）：

```sh
git clone http://127.0.0.1:8719/github/octocat/Hello-World.git
curl -o source.zip http://127.0.0.1:8719/github/octocat/Hello-World/archive/refs/heads/master.zip
curl -o asset.zip http://127.0.0.1:8719/github/OWNER/REPO/releases/download/TAG/asset.zip
curl http://127.0.0.1:8719/github/octocat/Hello-World/raw/master/README
```

也支持 `/releases/latest/download/文件名`。下载保留 Range、If-Range、ETag、Content-Range 与二进制响应，GitHub 的源码归档和 Release 重定向由服务端跟随，客户端无须直连重定向后的站点。

已有仓库可以使用 Git 的 `insteadOf`，只修改当前仓库的本地配置：

```sh
git config --local url."http://127.0.0.1:8719/github/".insteadOf "https://github.com/"
# 撤销
git config --local --remove-section 'url.http://127.0.0.1:8719/github/'
```

首次克隆直接使用代理地址即可。该规则也会匹配 HTTPS 推送地址；通过此服务推送会被拒绝，写入请使用独立的直连 push URL。

## 出站链路

- **自动选择**：优先直连，连接错误、超时或上游 5xx 时尝试另一条链路；未配置代理时只有直连。
- **仅直连**：直接访问 GitHub，不读取系统环境变量中的 HTTP 代理。
- **仅代理**：强制使用填写的代理，不回退到直连。

代理类型支持 HTTP CONNECT 和 SOCKS5（代理端解析目标域名）。地址格式为 `127.0.0.1:7890` 或 `[::1]:1080`，不含协议前缀。目前仅支持无需认证的代理，拒绝带用户名、密码的地址。

实际请求作为可达性探测：成功链路按仓库缓存 5 分钟（clone 的 `.git` 地址与下载地址共享仓库键），失败链路缓存 30 秒，最多缓存 1024 个仓库。失败缓存过期后，新仓库可重新尝试直连。切换只发生在下游尚未收到响应时；已经开始的下载不会拼接不同链路的数据，遇到截断会关闭连接，让客户端检测到失败并重试。

TCP 连接与 TLS 握手各限 5 秒，单次响应头等待限 8 秒，一次链路尝试的代理握手与所有重定向合计限 12 秒。响应内容按块转发，连续 2 分钟未读到数据才取消，不设置下载总时限。

## 支持边界

仅支持公开仓库的只读 Smart HTTP 克隆/拉取、源码归档、Release 文件和 raw 文件。保留 Git-Protocol 请求头以支持协议 v2，拒绝 `git-receive-pack`、推送及其他写入方法。不支持私有仓库认证、Git LFS、GitHub API 或通用网页代理。

仅允许 HTTPS 目标 `github.com`、`codeload.github.com`、`raw.githubusercontent.com`、`objects.githubusercontent.com`、`release-assets.githubusercontent.com`；重定向逐跳验证精确主机、端口及协议，最多 10 跳。入站 Authorization、Cookie、API Key、自定义凭据头不会出站，下载查询参数不透传。GitHub 自身产生的签名重定向参数会保留以下载 Release 资产。

协议依据：[Git Smart HTTP](https://git-scm.com/docs/http-protocol)、[GitHub Release 资产](https://docs.github.com/en/rest/releases/assets)、[GitHub 仓库内容](https://docs.github.com/en/rest/repos/contents)。Release 资产主机已通过真实下载验证。

## 验证

常规检查：

```powershell
go vet ./...
go test ./...
cd frontend
npm run build
```

可选真实网络验收（需要能够从开发机访问 GitHub）：

```powershell
$env:LOCALRELAY_GITHUB_E2E = '1'
go test ./internal/githubproxy -run TestGitHubIntegration -v -timeout 10m
Remove-Item Env:LOCALRELAY_GITHUB_E2E
```

验收使用临时目录和本地 HTTP CONNECT/SOCKS5 代理，不修改用户 Git 配置。四种场景分别验证直连、HTTP、SOCKS5、模拟直连故障后的自动回退：对 `git/git` 执行浅克隆与 `git fsck --full`，比较工作树版本，下载 shfmt Release 并比较 SHA-256，同时验证 archive 和 raw 下载。浅克隆包含该版本完整工作树，不下载仓库全部历史。
