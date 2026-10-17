# GPTibo Go 后端

Go + Gin + PostgreSQL，API 与业务 Worker 使用同一个二进制。此版本仅面向全新安装；匿名化后的迁移与原部署不兼容，不导入生产快照。

## Docker 本地启动

需要 Docker Compose，命令在本目录执行。首先生成三个独立随机秘密（Python 3）：

```sh
python3 scripts/init-secrets.py
cp .env.example .env
docker compose config --quiet
docker compose build api
docker compose up -d --wait postgres
docker compose --profile maintenance run --rm maintenance migrate
docker compose --profile maintenance run --rm maintenance grant-runtime
docker compose up -d --wait api worker
```

也可以在 PowerShell 使用 `./scripts/init-secrets.ps1` 初始化。秘密文件位于被忽略的 `.secrets/`，已有文件不会覆盖。API 仅监听宿主机 `127.0.0.1:8093`，PostgreSQL 不映射宿主机端口；运行数据库角色与迁移所有者分离。

`compose.auth.yaml` 可选启用管理员/邮件：配置 `ADMIN_USERNAME`、`ADMIN_EMAIL`、`MAIL_FROM`，自行生成 `.secrets/admin-totp` 并保存 `.secrets/resend-key`，再叠加该 Compose 文件。不要使用测试中的固定认证向量。

管理员也可使用独立的“用户名 + 密码 + 验证器动态码”模式，不依赖邮件服务。在本机通过隐藏输入把密码传给初始化工具（至少 12 字节），例如在本目录执行：

```sh
python -c "import getpass,subprocess; subprocess.run(['go','run','./cmd/admin-setup','--username','your-admin','--output','.admin-auth'],input=getpass.getpass('Admin password: ').encode(),check=True)"
```

工具拒绝覆盖已有目录，将 Argon2id 密码哈希、随机 TOTP 密钥和绑定 URI 写入 `.admin-auth/`，不会打印秘密。将 `password-hash`、`totp` 分别安全安装到 `.secrets/admin-password-hash` 和 `.secrets/admin-totp`，叠加 `compose.admin-password.yaml`，设置 `ADMIN_USERNAME`。原生部署可设置 `ADMIN_PASSWORD_HASH_FILE`、`ADMIN_TOTP_SECRET_FILE` 指向相应的受限文件。`ADMIN_EMAIL` 在密码模式下仅用于可选联系或告警。

验证器使用 SHA-1、6 位数字、30 秒周期；用本地二维码工具展示 `authenticator-uri.txt` 供手机绑定。密码及 TOTP 秘钥改变后旧管理员会话失效。启用密码模式后，邮件验证码入口不能作为替代登录途径；登录继续执行来源检查、可选 Turnstile、跨 IP 的账号限速及动态码防重放。密钥文件、绑定 URI 和二维码应保存在受限目录，不得提交或放入公共静态站点。

其他接入参数见 `.env.integrations.example`。复制为 `.env.integrations` 后可叠加 `compose.integrations.yaml`；所有开关默认关闭，匿名适配器的保留域名无法用于真实交易。必须先按自己的上游协议完成开发和测试，不能直接填生产秘密试运行。可选域名配置在 `compose.domains.yaml`，仅使用自己新建的云资源。

## 原生开发

程序读取环境变量，不自动加载 dotenv。通过 `DATABASE_URL_FILE` 或 `DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD_FILE` 指向独立数据库；外部连接默认要求 TLS。先用所有者身份执行 `migrate` 和 `grant-runtime`，再用受限角色执行 `api` 和 `worker`：

```sh
go run ./cmd/storefront migrate
go run ./cmd/storefront grant-runtime
go run ./cmd/storefront api
# 另一个终端使用相同运行配置：
go run ./cmd/storefront worker
```

`grant-runtime` 还需 `APP_DB_PASSWORD_FILE`。运行身份需 `AUTH_SECRET_FILE`，默认公开来源为 `http://127.0.0.1:8093`。配置管理员与邮件后才能使用完整后台登录。根目录执行 `npx vite --config services/storefront/vite.config.ts` 启动前端。

## 自备卡密库存

后台“卡密库存”页可按套餐粘贴卡密或上传 UTF-8 `.txt` 文件，每行一条、每批最多 50 条。系统会拒绝空批次、重复卡密和格式错误的整批内容；卡密经 AES-256-GCM 加密后写入数据库，后台只显示库存数量和批次信息，不提供明文回看或导出。不要把卡密放进日志、工单或命令行参数。

本地初始化脚本会在 `.secrets/` 创建独立的 `voucher-encryption-key` 和 `voucher-fingerprint-key`，Compose 以只读 secret 文件挂载给 API 与 Worker。两份密钥应与数据库备份分开、加密保存，并在恢复时一并取回。加密密钥丢失后，数据库中的卡密无法解密；更换任一密钥前，必须先开发并执行完整的数据重加密与指纹迁移流程，当前版本没有在线轮换工具。密钥未配置时，卡密导入和对应库存套餐的销售会关闭。

新订单会先预留一张库存卡密；只有支付渠道核实付款后，订单详情才向已验证的买家显示卡密。订单到期本身不会释放预留卡密，只有支付渠道确认交易关闭才会释放，以免顾客已付款但回调延迟时重复出售。若付款已核实但没有可分配库存，系统会保留订单并记录不含卡密的人工处理事件；商店需要通过订单和支持流程跟进，不能要求顾客重复付款。

## 测试

先在仓库根目录执行 `npm ci`；前后端路由契约测试需要 TypeScript 解析器。

```sh
go test ./...
go vet ./...
# 专用本机 PostgreSQL，库名必须以 _test 结尾：
STOREFRONT_TEST_DATABASE_URL='postgres://test_user:YOUR_LOCAL_PASSWORD@127.0.0.1:5432/storefront_test?sslmode=disable' go test -count=1 ./...
```

每个集成测试建立随机 schema 和受限角色，并在结束后清理，需要测试数据库所有者拥有创建角色权限。不可指向业务数据库；没有连接配置时，集成测试明确跳过。
