package inits

import (
	"context"
	"time"

	"schedule_server/global"
	"schedule_server/pkg/dingtalk"
)

// DingTalkInit 初始化钉钉客户端
func DingTalkInit() {
	cfg := global.AppConfig.DingTalk

	client, err := dingtalk.NewClient(cfg.AppKey, cfg.AppSecret)
	if err != nil {
		panic("初始化钉钉客户端失败: " + err.Error())
	}

	global.DingTalk = client

	// 启动时预获取 Token，验证配置是否正确
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token, err := global.DingTalk.GetAccessToken(ctx)
	if err != nil {
		panic("获取钉钉 AccessToken 失败: " + err.Error())
	}

	global.Log.Infof("钉钉客户端初始化完成, Token: %s...", token[:20])
}
