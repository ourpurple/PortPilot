# 从源码使用 Docker 部署 PortPilot

本说明适用于此 fork：<https://github.com/ourpurple/PortPilot>，包含节点前置代理功能。

使用项目根目录的 `compose.yaml` 在服务器上构建镜像。**原作者的 `maodeyu/portpilot:0.1.3` 镜像不包含本次新增功能，仅执行拉取镜像不能更新到此 fork 的代码。**

## 1. 部署前必读

- 本文命令在 **Linux 服务器的 Bash/sh** 中执行，不是在 Windows PowerShell 中执行。
- 需要 Git、Docker Engine、支持 BuildKit 的构建工具，以及 Docker Compose v2（命令为 `docker compose`）。Compose 应支持 `up --wait`；旧版请升级。
- 服务器无需单独安装 Go 或 Node，Dockerfile 会在构建阶段下载依赖、编译前端和后端。
- 构建需要能访问 GitHub、镜像仓库、npm 与 Go 模块源；请预留构建所需内存和磁盘空间。
- 以下示例统一使用项目名 **`portpilot-src`**。后续启动、更新、备份均使用相同的 `-p portpilot-src`，避免连接到另一套数据卷。
- 项目采用 Linux `host` 网络。新旧部署即使项目名不同，也不能同时占用相同的面板、控制器和代理入口端口。
- Docker 命令需要相应权限。若提示权限不足，使用具有权限的账户或按需使用 sudo；不要通过放宽 Docker socket 权限解决。

先检查工具：

```bash
git --version
docker version
docker compose version
docker buildx version
```

### 选择安装方式

| 场景 | 操作 |
| --- | --- |
| 全新部署，不需要旧节点和账号 | 按第 2–5 节操作 |
| 原部署使用 `./data:/app/data`，需要保留旧数据 | 按第 2–3 节准备和构建，然后走第 6 节迁移，不要先启动空安装 |
| 已按本文部署，需要更新源码 | 按第 7 节操作 |

**不要直接用源码 Compose 覆盖旧部署文件。** 源码版默认使用 Docker 命名卷，旧部署如果使用 `./data` 目录挂载，两者不是同一个数据位置。

## 2. 获取源码和配置环境

### 2.1 获取此 fork

以下路径可自行更换；确保当前账户对目标目录有写入权限：

```bash
git clone https://github.com/ourpurple/PortPilot.git /opt/portpilot-src
cd /opt/portpilot-src
git log -1 --oneline
```

确认使用的是仓库根目录的 `compose.yaml`，其中应包含：

```yaml
build:
  context: .
```

不要改用 `compose.release.yaml`，它是预构建镜像部署配置。

如果是上传本地源码，需完整上传 `Dockerfile`、`.dockerignore`、`compose.yaml`、`.env.example`、`go.mod`、`go.sum`、`cmd/`、`internal/` 和 `web/`。无需上传 `data/`、本机 `.env`、`web/node_modules/` 或 `web/dist/`。保留 `.dockerignore`，避免无关数据和本机依赖进入镜像构建上下文。

### 2.2 创建环境文件

仅在没有 `.env` 时复制，避免覆盖已有密码或配置：

```bash
cd /opt/portpilot-src
umask 077
if [ ! -f .env ]; then cp .env.example .env; fi
chmod 600 .env
nano .env
```

建议按下面的说明调整现有条目，不要在同一个文件里重复添加同名变量：

```dotenv
# 留空：首次访问面板时设置管理密码。
# 也可设置至少 12 个字符的独立强密码；不要使用代理入口密码。
PORTPILOT_ADMIN_PASSWORD=

# 推荐只监听本机，通过 SSH 隧道或 HTTPS 反向代理访问。
PORTPILOT_LISTEN=127.0.0.1:8787

# 改为服务器的真实公网 IP 或域名，不包含协议、端口或路径。
PORTPILOT_PUBLIC_HOST=你的服务器公网IP

# Mihomo 控制器保留本机监听，不要向公网开放。
PORTPILOT_CONTROLLER_URL=http://127.0.0.1:9090

# 留空：使用 portpilot 用户名和自动生成的代理密码。
PORTPILOT_PROXY_USERNAME=
PORTPILOT_PROXY_PASSWORD=

# 保持现有内核版本，内核升级与面板更新分开进行。
MIHOMO_IMAGE=metacubex/mihomo:v1.19.32

# 从源码构建本地镜像，不填写原作者的镜像仓库。
PORTPILOT_IMAGE_REPOSITORY=portpilot
PORTPILOT_IMAGE_TAG=local

# 默认 Go 模块下载源；连接超时时可按第 10 节调整。
PORTPILOT_GOPROXY=https://proxy.golang.org,direct
```

注意：

- `PORTPILOT_PUBLIC_HOST`、控制器和代理入口账号密码等初始化变量只用于新安装。已有数据的配置应在 Web 面板修改，并按需应用。
- 管理密码留空不会清除已有密码，也不会重置数据。
- 若必须直接从公网访问面板，可将 `PORTPILOT_LISTEN` 改为 `0.0.0.0:8787`；应限制管理来源 IP，并优先配置 HTTPS。首次密码设置页面也必须受到访问保护。
- 代理入口账号密码可在面板“访问认证”页面查看。

## 3. 构建镜像

```bash
cd /opt/portpilot-src

docker compose -p portpilot-src config --quiet
docker compose -p portpilot-src build portpilot
docker compose -p portpilot-src pull mihomo
```

配置检查应无错误，镜像构建和内核镜像拉取成功后再继续。构建会自动执行前端构建和 Go 编译，不需要在宿主机手动运行 npm 或 go。

**只有构建成功才继续切换服务；构建失败不会影响仍在运行的旧部署。** 不需要日常使用 `--no-cache`。

## 4. 全新部署启动

> 如果需要旧数据，请跳到第 6 节，先导入数据，不要执行本节启动空安装。

### 4.1 停止占用相同端口的旧部署

若旧服务仍在运行，在旧部署目录执行：

```bash
cd /你的原部署目录
docker compose stop portpilot mihomo
```

如旧项目通过 `-p` 指定过项目名，停止时也必须使用对应项目名。保留旧数据和配置，不要删除它们，也不要执行 `down -v`。

### 4.2 启动源码部署

```bash
cd /opt/portpilot-src

docker compose -p portpilot-src up -d --build --wait
docker compose -p portpilot-src ps -a
docker compose -p portpilot-src logs --tail=100 init portpilot mihomo
```

正常状态：

| 服务 | 预期状态 |
| --- | --- |
| `init` | `Exited (0)`，初始化成功后退出，属于正常现象 |
| `portpilot` | 运行中，健康检查通过 |
| `mihomo` | 运行中 |

源码 Compose 的数据挂载是 `data:/app/data`。按本文项目名启动后，通常对应 Docker 卷 `portpilot-src_data`，不会自动读取旧目录里的 `./data`。

**只有首次使用且没有现有数据的卷才是空安装。** 换目录并不一定创建新数据，项目名相同仍可能使用原命名卷。不要为了“重装”直接删除不明用途的数据卷。

## 5. 访问面板与首次使用

### 推荐：通过 SSH 隧道访问

若使用 `PORTPILOT_LISTEN=127.0.0.1:8787`，在你自己的电脑终端执行：

```bash
ssh -N -L 18787:127.0.0.1:8787 你的SSH用户@你的服务器IP
```

保持连接，浏览器访问：

```text
http://127.0.0.1:18787
```

若配置了公网监听，则通过 `http://服务器IP:8787` 或你的 HTTPS 反向代理访问。

### 使用顺序

1. 管理密码留空的新安装，首次访问时创建管理密码。
2. 导入订阅或添加独立节点。
3. 检查公网地址、固定入口端口范围、代理认证和 Mihomo 控制器连接。
4. 需要前置代理时：进入“节点管理 → 前置代理”，添加一个代理配置，再单节点或批量绑定。
5. 点击“应用配置”。
6. 在服务器防火墙和云安全组中放行实际使用的代理入口端口，再进行连接测试和出口 IP 查询。

默认代理入口端口范围为 `20001–20999`；以面板实际设置为准。使用 UDP 时还需要放行相应 UDP 端口，并确认链路协议支持。**不要放行 9090 控制器端口。**

绑定后的链路：

```text
Mihomo → 前置代理 → 原节点 → 目标网站
```

删除或解绑前置代理后，改为直接连接原节点，不是绕过原节点访问目标。影响链路的保存操作仍需手动应用；前置代理失效不会自动回退。

## 6. 从旧的 ./data 部署迁移，保留数据

本节适用于旧 Compose 使用 `./data:/app/data` 的情况。若旧部署使用命名卷，请使用原部署的卷备份方式，不要假设数据位于目录中。

目标源码、`.env` 和镜像需先按第 2–3 节准备好。迁移时会短暂停机。

### 6.1 停止旧服务并备份整个数据目录

在旧部署目录执行，将 `/opt/portpilot-old` 替换为真实路径：

```bash
cd /opt/portpilot-old
umask 077
BACKUP="$(pwd)/backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP"
cp -p compose.yaml "$BACKUP/compose.yaml"
if [ -f .env ]; then cp -p .env "$BACKUP/portpilot.env"; fi

docker compose stop portpilot mihomo
sudo tar -czpf "$BACKUP/data.tar.gz" data
printf '备份位置：%s\n' "$BACKUP"
```

确认备份成功且归档可读取后再继续：

```bash
sudo tar -tzf "$BACKUP/data.tar.gz" > /dev/null
```

备份包含真实节点密钥和账号，请勿上传到 GitHub。应完整保留 `state.json`、已有的 `admin.json` 和已应用的 `mihomo.yaml`，不要只复制单个状态文件。

### 6.2 确认目标卷为空，再导入

回到源码目录：

```bash
cd /opt/portpilot-src

# 创建/挂载目标卷并检查是否为空，不执行面板和初始化程序。
docker compose -p portpilot-src run --rm -T --no-deps \
  --entrypoint sh portpilot \
  -c 'set -e; entries=$(ls -A /app/data); test -z "$entries"'
```

**此检查必须成功（退出码为 0）才能导入。** 失败说明目标已有数据，停止迁移并检查卷和项目名；不要直接覆盖或删除。

把下一条命令中的归档路径替换为第 6.1 节输出的真实绝对路径：

```bash
sudo cat /opt/portpilot-old/backups/实际时间目录/data.tar.gz | \
  docker compose -p portpilot-src run --rm -T --no-deps \
    --entrypoint tar portpilot -xozf - -C /app
```

导入容器以 UID/GID `10001:10001` 运行；`tar -o` 不恢复旧所有者，使导入文件归运行用户所有。不需要把数据目录设置成 `777`。

**确认导入命令成功**，再启动：

```bash
docker compose -p portpilot-src up -d --build --wait
docker compose -p portpilot-src ps -a
docker compose -p portpilot-src logs --tail=100 init portpilot mihomo
```

登录后检查旧节点、订阅、账号和固定端口是否保留。迁移成功前保留旧目录、备份和旧容器；不要让两套服务同时运行。

如果导入或启动失败，先停止新部署，再处理问题或启动旧服务。不要对已有数据反复叠加解压。

## 7. 后续更新源码

### 7.1 更新前备份

按第 8 节备份，并记录当前源码版本：

```bash
cd /opt/portpilot-src
git rev-parse HEAD
```

保留当前运行镜像，便于回退：

```bash
CID=$(docker compose -p portpilot-src ps -q portpilot)
OLD_IMAGE=$(docker inspect --format '{{.Image}}' "$CID")
docker image tag "$OLD_IMAGE" portpilot:before-update
```

`before-update` 是滚动备份标签，每次会覆盖它。若需保留多个版本，改用独立时间戳标签。

### 7.2 拉取源码并更新面板

```bash
cd /opt/portpilot-src
git status --short
```

有本地修改时先处理或备份，不使用 `git reset --hard` 清理。工作区干净后：

```bash
git pull --ff-only origin main

docker compose -p portpilot-src up -d --no-deps --build --wait portpilot
docker compose -p portpilot-src ps -a
docker compose -p portpilot-src logs --tail=100 portpilot
```

这会重新构建并替换面板，不删除数据卷，也不重建 Mihomo。浏览器强制刷新后检查新功能。

**仅执行 `docker compose restart` 不会重新构建镜像，也不会加载新代码。**

Mihomo 不随面板源码更新而自动升级；需要升级时，单独评估并修改 `MIHOMO_IMAGE`，在维护窗口操作。

## 8. 数据备份

以下命令备份此源码部署的命名卷。为便于恢复一致性，备份期间停止面板和内核，会短暂中断代理连接：

```bash
cd /opt/portpilot-src
umask 077
BACKUP="$(pwd)/backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP"
cp -p compose.yaml "$BACKUP/compose.yaml"
cp -p .env "$BACKUP/portpilot.env"
git rev-parse HEAD > "$BACKUP/source-commit.txt"

docker compose -p portpilot-src stop portpilot mihomo

docker compose -p portpilot-src run --rm -T --no-deps \
  --entrypoint tar portpilot \
  -czf - -C /app data > "$BACKUP/data.tar.gz"
```

确认归档命令成功，再检查文件：

```bash
tar -tzf "$BACKUP/data.tar.gz" > /dev/null

docker compose -p portpilot-src start mihomo portpilot
printf '备份位置：%s\n' "$BACKUP"
```

备份失败也不要忘记按需启动原服务；不要把失败或不完整的归档视为可用备份。`backups/` 内包含凭据，应保存在受保护的位置，并另存到可靠备份介质。

恢复时必须使用**空目标卷**：先准备匹配备份版本的源码和镜像，停止占用相同端口的服务，再按第 6.2 节的空卷检查和解压方式恢复，并还原合适的 `.env`。不要在运行中的非空卷上直接解压覆盖。

## 9. 回退与卸载注意事项

### 9.1 仅回退面板镜像

适用于仅更新程序且不需要恢复数据的情况。创建 `compose.rollback.yaml`：

```yaml
services:
  init:
    image: portpilot:before-update
  portpilot:
    image: portpilot:before-update
```

执行：

```bash
docker compose -p portpilot-src \
  -f compose.yaml -f compose.rollback.yaml \
  up -d --no-deps --no-build --pull never --wait portpilot
```

这不会回退数据，也不会撤销已应用到 Mihomo 的配置。旧版本面板不支持前置代理管理时，优先在新版中解绑并应用后再回退；需要完全恢复旧状态时，应恢复更新前的完整数据备份。

回退期间后续操作应继续使用相同的 `-f` 参数；直接使用基础 Compose 可能再次切回新镜像。回退文件只保存在服务器本地，不包含在源码交付中。

### 9.2 停止与删除容器

```bash
# 停止，保留容器和数据。
docker compose -p portpilot-src stop

# 删除此项目容器和网络，保留命名数据卷。
docker compose -p portpilot-src down
```

**不要误执行 `docker compose -p portpilot-src down -v`。** `-v` 会删除此项目声明的数据卷，导致节点、配置和账号数据丢失。删除数据前必须有明确意图和可用备份。

## 10. 常见问题

### Go 依赖下载超时

将 `.env` 的模块源改为：

```dotenv
PORTPILOT_GOPROXY=https://goproxy.cn,direct
```

然后重新构建。该设置只影响 Go 模块，不解决 Docker 基础镜像或 npm 源的网络问题，也不关闭模块校验。

### 构建时内存不足

检查服务器内存和磁盘，必要时使用更充足的构建机器或合理配置交换空间。不要通过删除数据卷释放空间，也不要直接运行会清理数据卷的 prune 命令。

### 提示端口占用

检查旧面板、旧 Mihomo、其他代理服务和 systemd 服务。host 网络下修改 Compose 项目名不能解除端口冲突。先停止冲突服务，不要为了启动而暴露控制器到公网。

### 出现首次设置页面，旧节点都不见了

通常是新项目使用了空命名卷，而旧部署使用 `./data` 或另一个项目名。先核对数据位置，不要继续初始化或删除旧数据；按第 6 节迁移。

### `init` 因已有节点却缺少 mihomo.yaml 而退出

恢复完整数据备份。初始化会保留已有已应用配置，不会擅自将待应用状态变为线上配置；不要删除 state.json 来绕过检查。

### 页面没有前置代理按钮

确认 clone 的是 `ourpurple/PortPilot`，已拉取包含前置代理功能的源码，并执行带 `--build` 的更新命令。检查本地镜像变量没有指向原作者镜像，然后强制刷新浏览器。

### 新代码已部署，但代理链路没变化

保存面板数据不等于应用到内核。绑定或修改连接配置后，需要点击“应用配置”。进一步检查内核连接、代理参数、协议兼容性、防火墙和实际链路。

## 11. 验证范围说明

本文部署步骤已对照当前 `compose.yaml`、`Dockerfile`、环境变量和初始化代码核对；编写文档时未在 Linux 服务器执行 Docker 构建、迁移、备份或恢复。

前置代理代码已通过新增后端测试和浏览器交互验证，但真实 Mihomo 内核和有效代理凭据的网络链路仍需部署后确认。详细记录见 [实施计划与验证记录](../plan.md)；功能变更见 [更新日志](../CHANGELOG.md)。
