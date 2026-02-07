package inits

import (
	"fmt"
	"os"

	"schedule_server/global"

	"github.com/spf13/viper"
)

// ConfigInit 初始化配置文件并解析到全局变量
func ConfigInit() {
	// 从环境变量读取配置文件名，默认为 dev
	configName := os.Getenv("CONFIG_ENV")
	if configName == "" {
		configName = "dev"
	}

	viper.SetConfigName(configName)  // 配置文件名（不含扩展名）
	viper.SetConfigType("yaml")      // 配置文件类型
	viper.AddConfigPath("./configs") // 查找配置文件所在路径
	viper.AddConfigPath(".")         // 工作目录备选

	if err := viper.ReadInConfig(); err != nil {
		panic(fmt.Sprintf("读取配置文件失败: %v", err))
	}

	if err := viper.Unmarshal(&global.AppConfig); err != nil {
		panic(fmt.Sprintf("解析配置文件失败: %v", err))
	}

	fmt.Printf("[配置] 已加载: %s\n", viper.ConfigFileUsed())
}
