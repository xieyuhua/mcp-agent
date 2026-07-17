package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// geocodingResult Open-Meteo 地理编码返回（取第一个匹配城市）。
type geocodingResult struct {
	Results []struct {
		Name      string  `json:"name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Country   string  `json:"country"`
		Admin1    string  `json:"admin1"`
	} `json:"results"`
}

// weatherResponse Open-Meteo 当前天气返回（精简字段）。
type weatherResponse struct {
	Current struct {
		Temperature   float64 `json:"temperature_2m"`
		Humidity      int     `json:"relative_humidity_2m"`
		WindSpeed     float64 `json:"wind_speed_10m"`
		WeatherCode   int     `json:"weather_code"`
		IsDay         int     `json:"is_day"`
		Time          string  `json:"time"`
	} `json:"current"`
	CurrentUnits struct {
		Temperature string `json:"temperature_2m"`
		WindSpeed   string `json:"wind_speed_10m"`
	} `json:"current_units"`
}

// weatherCodeText 把 WMO 天气代码翻译为中文描述。
func weatherCodeText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1:
		return "大致晴朗"
	case 2:
		return "局部多云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55:
		return "毛毛雨"
	case 56, 57:
		return "冻毛毛雨"
	case 61, 63, 65:
		return "雨"
	case 66, 67:
		return "冻雨"
	case 71, 73, 75:
		return "雪"
	case 77:
		return "雪粒"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95:
		return "雷阵雨"
	case 96, 99:
		return "雷阵雨伴冰雹"
	default:
		return "未知天气"
	}
}

// queryWeather 联网查询指定城市的实时天气（使用 Open-Meteo 免费接口，无需 API key）。
// 返回格式化的中文天气描述，供大模型直接使用。
func (a *Agent) queryWeather(city string) (string, error) {
	city = strings.TrimSpace(city)
	if city == "" {
		return "", fmt.Errorf("城市名不能为空")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// 1) 地理编码：城市名 -> 经纬度
	geoURL := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=zh",
		url.QueryEscape(city))
	geoBody, err := getHTTP(client, geoURL)
	if err != nil {
		return "", fmt.Errorf("地理编码请求失败: %w", err)
	}
	var geo geocodingResult
	if err := json.Unmarshal(geoBody, &geo); err != nil {
		return "", fmt.Errorf("解析地理编码结果失败: %w", err)
	}
	if len(geo.Results) == 0 {
		return "", fmt.Errorf("未找到城市: %s", city)
	}
	loc := geo.Results[0]

	// 2) 实时天气：经纬度 -> 当前天气
	wxURL := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,wind_speed_10m,weather_code,is_day&timezone=auto",
		loc.Latitude, loc.Longitude)
	wxBody, err := getHTTP(client, wxURL)
	if err != nil {
		return "", fmt.Errorf("天气请求失败: %w", err)
	}
	var wx weatherResponse
	if err := json.Unmarshal(wxBody, &wx); err != nil {
		return "", fmt.Errorf("解析天气结果失败: %w", err)
	}

	region := loc.Name
	if loc.Admin1 != "" {
		region = loc.Admin1 + " " + loc.Name
	}
	if loc.Country != "" {
		region += " (" + loc.Country + ")"
	}

	desc := fmt.Sprintf(
		"%s 当前天气：%s，气温 %.1f%s，相对湿度 %d%%，风速 %.1f%s，观测时间 %s。",
		region,
		weatherCodeText(wx.Current.WeatherCode),
		wx.Current.Temperature,
		wx.CurrentUnits.Temperature,
		wx.Current.Humidity,
		wx.Current.WindSpeed,
		wx.CurrentUnits.WindSpeed,
		wx.Current.Time,
	)
	return desc, nil
}

// getHTTP 发起 GET 请求并返回响应体。
func getHTTP(client *http.Client, u string) ([]byte, error) {
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
