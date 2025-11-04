package serverchanturbo

import (
    "github.com/komari-monitor/komari/utils/messageSender/factory"
)

// Addition 为 Server酱 Turbo 推送通道的配置项
// 仅允许配置接口地址及可选的通道/隐藏IP/openid，固定以 JSON 发送
type Addition struct {
    // APIURL 为接口完整地址，例如：https://sctapi.ftqq.com/<sendkey>.send
    APIURL  string `json:"api_url" required:"true" help:"接口完整地址，例如 https://sctapi.ftqq.com/<sendkey>.send；参考：https://sct.ftqq.com/"`
    // Channel 为本次推送使用的消息通道，最多两个，多个用 | 隔开，例如：9|66
    Channel string `json:"channel" help:"消息通道，可选，多个用 | 隔开，例如 9|66"`
    // NoIP 是否隐藏调用 IP，填 1 则隐藏
    NoIP    string `json:"noip" help:"是否隐藏调用IP，填 1 隐藏；为空则不隐藏"`
    // OpenID 消息抄送 openid，测试号用 , 分隔；企业微信应用用 | 分隔
    OpenID  string `json:"openid" help:"抄送 openid，测试号用 , 分隔；企业微信应用用 | 分隔"`
}

// 注册 Server酱 Turbo 推送通道到工厂
func init() {
    factory.RegisterMessageSender(func() factory.IMessageSender {
        return &ServerChanTurboSender{}
    })
}