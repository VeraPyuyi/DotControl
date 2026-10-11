# 可选网关

默认每台设备独立插件和隧道即可使用。只有需要在一个入口列出多台设备时才运行网关。网关也在自己的常开电脑上运行，使用独立状态目录和独立官方隧道。关闭网关不会关闭设备自己的隧道。

先在两台已登录电脑安装 Tailscale 并配置允许相互通信的访问规则。DotControl 接受字面 Tailscale IP（IPv4 100.64/10 或 IPv6 fd7a:115c:a1e0::/48），不用可变 DNS 名称。不自动配置防火墙、VPN 或 Tailscale 策略。

设备与网关分别运行 `dotcontrol pair init`，得到自己的公开 `device.crt` 和 SHA-256 指纹。只交换公有证书，在可信渠道比对指纹；私钥留在各自系统凭据库。

设备配置文件添加 `gateway_listen`，值为本机精确 Tailscale IP 加端口，例如 `100.70.1.2:9443`。在设备上导入网关证书：

```sh
dotcontrol pair add --role controller --cert gateway.crt --name '常开网关'
```

在网关上导入设备证书及目的地址：

```sh
dotcontrol pair add --role device --cert device.crt --url https://100.70.1.2:9443 --name '办公室 Windows'
```

设备运行 `dotcontrol serve`；网关用它自己的配置运行 `dotcontrol serve --gateway`。网关无需桌面执行器，设备仍必须有可用执行器及活动监控。为网关创建另一个私有插件，devices 只列出已配对设备。某设备离线不会被路由到其他设备。

双向 TLS 固定双方证书和设备身份，所有六类请求携带精确 device_id。取消、动作记录和执行互斥由目的设备统一管理，因此独立入口和网关不能同时注入同一设备。网关不会在超时后自动重放请求。

`dotcontrol pair list` 查看配对，`dotcontrol pair revoke DEVICE_ID` 撤销一侧信任。建议在双方分别撤销。每次请求重新检查信任，已有 TLS 长连接不能绕过撤销。

当前测试覆盖真实本机 TLS 连接、错误设备身份、假冒证书、撤销后复用连接、离线设备、禁止重定向及共享动作后端。真实 Tailscale 双电脑操作仍未验收。
