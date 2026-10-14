# Phone Upload

一个简单、轻量的局域网文件上传工具。电脑启动服务后，在手机浏览器中打开页面，即可一次上传多个照片、视频或文档到电脑。

- 只使用 Go 标准库，无第三方依赖
- 支持一次选择和上传多个文件
- 适配手机屏幕，显示上传进度
- 支持点击选择和拖放上传
- 同名文件自动编号，不会覆盖已有文件
- 清理危险文件名，防止目录穿越
- 可配置监听地址、保存目录和上传大小上限

## 使用场景

适合在家庭或办公室的可信局域网中，临时把手机文件传到电脑。项目默认不提供登录、加密或公网访问保护，请不要直接暴露到互联网。

## 环境要求

- Go 1.22 或更高版本
- 手机和电脑连接到同一个局域网

## 快速开始

进入项目目录并启动：

```bash
go run .
```

终端会输出可访问地址，例如：

```text
文件将保存到: /path/to/phone-upload/uploads
本机打开: http://localhost:8080
手机打开: http://192.168.1.23:8080
```

在手机浏览器中打开 `手机打开` 后面的地址，选择文件并上传。文件默认保存在项目目录下的 `uploads/` 中。

按 `Ctrl+C` 停止服务。

## 编译运行

也可以先编译为单个可执行文件：

```bash
go build -o phone-upload .
./phone-upload
```

Windows：

```powershell
go build -o phone-upload.exe .
.\phone-upload.exe
```

## 下载预编译版本

在 GitHub 的 Releases 页面可以下载已编译好的二进制文件，无需安装 Go：

| 系统 | 文件 |
| --- | --- |
| Windows x64 / ARM64 | `phone-upload_<版本>_windows_amd64.zip` / `..._windows_arm64.zip` |
| Linux x64 / ARM64 | `phone-upload_<版本>_linux_amd64.tar.gz` / `..._linux_arm64.tar.gz` |
| macOS Intel / Apple Silicon | `phone-upload_<版本>_darwin_amd64.tar.gz` / `..._darwin_arm64.tar.gz` |

`SHA256SUMS.txt` 中提供了各文件的校验和。macOS 首次运行如被系统拦截，可执行 `xattr -d com.apple.quarantine ./phone-upload` 后再运行。

### 发布新版本（维护者）

推送 `v` 开头的标签即可触发 `.github/workflows/release.yml`，自动测试、交叉编译并创建 Release：

```bash
git tag v1.0.0
git push origin v1.0.0
```

也可以在 GitHub 的 Actions 页面手动运行 `Release` 工作流并填写标签。

## 配置参数

```text
Usage of phone-upload:
  -addr string
        监听地址，例如 :8080 (default ":8080")
  -dir string
        文件保存目录 (default "uploads")
  -max-upload int
        单次请求的最大总大小（MiB），0 表示不限制
```

示例：监听 `9000` 端口、保存到指定目录，并限制每次最多上传 4096 MiB：

```bash
./phone-upload -addr :9000 -dir /path/to/files -max-upload 4096
```

默认 `-max-upload 0`，即应用不主动限制上传总大小。实际可上传大小仍受磁盘空间、操作系统和网络环境影响。

## 文件保存规则

- 上传目录不存在时会自动创建。
- 已存在同名文件时，新文件会依次保存为 `文件名 (1).ext`、`文件名 (2).ext` 等，不会覆盖原文件。
- 如果某次受限上传超过配置大小，该次请求写入的文件会被清理。
- 上传成功后，服务端会返回每个文件的原始名称、实际保存名称和字节数。

## API

上传接口为 `POST /upload`，请求格式是 `multipart/form-data`，文件字段名为 `files`，可重复传入该字段：

```bash
curl -F "files=@photo.jpg" -F "files=@video.mp4" http://localhost:8080/upload
```

成功响应示例：

```json
{
  "ok": true,
  "files": [
    {
      "original": "photo.jpg",
      "savedAs": "photo.jpg",
      "size": 123456
    }
  ],
  "message": "已成功上传 1 个文件"
}
```

## 常见问题

### 手机打不开页面

请依次确认：

1. 手机和电脑连接到同一个 Wi-Fi 或局域网。
2. 使用终端中显示的局域网 IP，而不是 `localhost`。
3. 电脑防火墙允许对应端口，默认是 TCP `8080`。
4. 路由器没有启用“客户端隔离”“AP 隔离”或“访客网络隔离”。

### 上传大文件失败

确认保存磁盘有足够空间。如果使用了反向代理，还需检查代理自身的请求大小和超时限制。

### 终端显示多个手机访问地址

电脑可能同时连接了 Wi-Fi、网线或虚拟网络。通常选择与手机处于相同网段的地址即可，例如手机地址是 `192.168.1.x`，就选择同样以 `192.168.1.` 开头的地址。

## 安全说明

服务默认监听所有网络接口，并且没有身份认证。局域网内能够访问该端口的设备都可以上传文件，因此：

- 仅在可信网络中运行；
- 不要把端口直接映射到公网；
- 使用完毕后及时停止服务；
- 如需公网使用，请在前方配置 HTTPS、身份认证、上传限额和访问控制。

## 开发

运行测试和静态检查：

```bash
go test ./...
go vet ./...
```

提交贡献前，请确保代码已经通过 `gofmt`、测试和静态检查。欢迎通过 Issue 报告问题，或通过 Pull Request 提交改进。

## License

发布者尚未为本项目指定开源许可证。公开发布前，请添加合适的 `LICENSE` 文件；在许可证确定之前，默认版权仍归项目作者所有。
