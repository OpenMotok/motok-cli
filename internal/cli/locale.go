package cli

import "strings"

func chineseError(e *cliError) string {
	messages := map[string]string{
		"config_path":            "无法确定用户配置目录。",
		"config_read":            "无法读取配置文件。",
		"config_write":           "无法写入配置文件。",
		"config_invalid":         "配置格式或字段无效，请检查示例配置。",
		"route_missing":          "请在 routes 中配置所需能力对应的渠道。",
		"route_invalid":          "能力类型必须为 text、image 或 video，且不能与命令冲突。",
		"channel_missing":        "路由引用的渠道尚未配置。",
		"provider_invalid":       "protocol 必须为 openai、anthropic 或 gemini。",
		"url_invalid":            "服务地址必须是 HTTP(S) URL，且不能包含用户名、密码、查询参数或片段。",
		"key_invalid":            "API Key 包含无效空白字符。",
		"key_missing":            "请设置 配置对应的密钥环境变量，或在配置中填写 api_key。",
		"output_write":           "无法写入文件；文件可能已经存在。",
		"output_exists":          "输出文件已存在，请更换文件名。",
		"output_missing":         "请用 --output 或 -o 指定输出文件。",
		"request_invalid":        "无法创建请求。",
		"request_cancelled":      "请求已取消或超时。",
		"connection_failed":      "无法连接配置的 API 服务。",
		"response_too_large":     "API 响应超过大小限制，或读取中断。",
		"response_invalid":       "API 返回了无效或不符合预期的响应。",
		"image_invalid":          "图片数据编码无效。",
		"image_missing":          "响应中没有可下载的图片。",
		"download_url_invalid":   "下载地址必须使用 HTTPS；本机地址允许 HTTP。",
		"download_failed":        "文件下载失败或中断。",
		"model_invalid":          "模型标识无效。",
		"model_missing":          "请在 配置中设置 model，或使用 --model。",
		"prompt_missing":         "请使用 --prompt 提供提示词，或用 --prompt - 从标准输入读取。",
		"input_invalid":          "输入无效、无法读取或超过大小限制。",
		"file_read":              "无法读取参考图片。",
		"file_missing":           "图片编辑需要通过 --file 提供参考图片。",
		"image_type":             "参考图片必须为 PNG、JPEG 或 WebP。",
		"capability_unsupported": "所选协议不支持此辅助命令，请查看支持范围或使用原生 request。",
		"capability_unavailable": "该官方服务的此接口当前不可用，请配置实际支持该能力的兼容服务。",
		"text_missing":           "响应中没有文本，请使用 --json 查看原始响应。",
		"id_invalid":             "任务编号无效。",
		"path_invalid":           "请求路径必须是以 / 开头的相对 API 路径。",
		"method_invalid":         "不支持此 HTTP 方法。",
		"option_unknown":         "未知选项；密钥和 URL 应通过配置文件或环境变量设置。",
		"option_value":           "选项缺少参数值。",
		"option_invalid":         "选项值或组合无效，请查看 help。",
		"language_invalid":       "语言必须为 en 或 zh。",
		"command_unknown":        "未知命令，请使用 motok help 查看用法。",
		"operation_failed":       "操作失败。",
	}
	if e.Code == "api_error" {
		return "API 请求失败（" + e.Message + "）"
	}
	if e.Code == "video_timeout" || e.Code == "video_failed" {
		parts := strings.Fields(e.Message)
		id := ""
		if len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		if e.Code == "video_timeout" {
			return "视频等待已中断，可通过 video status 查询任务：" + id
		}
		return "视频任务未能完成：" + id
	}
	if message, ok := messages[e.Code]; ok {
		return message
	}
	return e.Message
}
