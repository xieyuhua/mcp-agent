package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WeatherResult 天气查询结果。
type WeatherResult struct {
	Location   string `json:"location"`
	Country    string `json:"country"`
	Condition  string `json:"condition"`
	TempC      string `json:"temp_c"`
	FeelsLikeC string `json:"feels_like_c"`
	Humidity   string `json:"humidity"`
	Pressure   string `json:"pressure"`
	WindSpeed  string `json:"wind_speed"`
	WindDir    string `json:"wind_dir"`
	Text       string `json:"text"`
}

// WeatherService 天气查询能力：使用 wttr.in 免费服务（无需 API key）。
type WeatherService struct {
	client    *http.Client
	userAgent string
}

// NewWeatherService 构造天气查询服务。
func NewWeatherService() *WeatherService {
	return &WeatherService{
		client:    &http.Client{Timeout: 15 * time.Second},
		userAgent: "curl/8.0",
	}
}

// Query 查询指定城市的实时天气（温度、体感温度、天气状况、湿度、气压、风速）。
func (w *WeatherService) Query(_ context.Context, location string, onProgress ProgressFunc) (*WeatherResult, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return nil, fmt.Errorf("缺少 location 参数，请提供城市名")
	}
	if onProgress != nil {
		onProgress(0, "正在查询天气: "+location)
	}

	start := time.Now()
	u := fmt.Sprintf("https://wttr.in/%s?format=j1", url.PathEscape(location))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("构造天气请求失败: %w", err)
	}
	req.Header.Set("User-Agent", w.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("天气服务不可达: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("天气服务返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取天气响应失败: %w", err)
	}

	var data struct {
		CurrentCondition []struct {
			TempC       string `json:"tempC"`
			FeelsLikeC  string `json:"FeelsLikeC"`
			Humidity    string `json:"humidity"`
			WeatherDesc []struct {
				Value string `json:"value"`
			} `json:"weatherDesc"`
			WindSpeedKmph string `json:"windspeedKmph"`
			WindDir       string `json:"winddir16Point"`
			Pressure      string `json:"pressure"`
		} `json:"current_condition"`
		NearestArea []struct {
			AreaName []struct {
				Value string `json:"value"`
			} `json:"areaName"`
			Country []struct {
				Value string `json:"value"`
			} `json:"country"`
		} `json:"nearest_area"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("解析天气响应失败: %w", err)
	}
	if len(data.CurrentCondition) == 0 {
		return nil, fmt.Errorf("未获取到 %s 的天气数据，请检查城市名", location)
	}

	cur := data.CurrentCondition[0]
	area := location
	country := ""
	if len(data.NearestArea) > 0 {
		if len(data.NearestArea[0].AreaName) > 0 {
			area = data.NearestArea[0].AreaName[0].Value
		}
		if len(data.NearestArea[0].Country) > 0 {
			country = data.NearestArea[0].Country[0].Value
		}
	}

	desc := "未知"
	if len(cur.WeatherDesc) > 0 && cur.WeatherDesc[0].Value != "" {
		desc = cur.WeatherDesc[0].Value
	}

	loc := area
	if country != "" && country != "China" {
		loc = fmt.Sprintf("%s(%s)", area, country)
	}

	text := fmt.Sprintf(
		"%s 当前天气：%s\n温度：%s°C（体感 %s°C）\n湿度：%s%%\n气压：%s hPa\n风速：%s km/h %s",
		loc, desc, cur.TempC, cur.FeelsLikeC, cur.Humidity, cur.Pressure, cur.WindSpeedKmph, cur.WindDir,
	)

	log.Printf("[query_weather] location=%s duration=%s", location, time.Since(start))
	if onProgress != nil {
		onProgress(100, "天气查询完成")
	}

	return &WeatherResult{
		Location:   loc,
		Country:    country,
		Condition:  desc,
		TempC:      cur.TempC,
		FeelsLikeC: cur.FeelsLikeC,
		Humidity:   cur.Humidity,
		Pressure:   cur.Pressure,
		WindSpeed:  cur.WindSpeedKmph,
		WindDir:    cur.WindDir,
		Text:       text,
	}, nil
}
