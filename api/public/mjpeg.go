package public

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api/jsonRpc"
	conf "github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/models"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	fontURL      = "https://hyperos.mi.com/font-download/MiSans.zip"
	fontPath     = "./data/font.ttf"
	fontZipEntry = "MiSans/ttf/MiSans-Normal.ttf"

	// 渲染参数
	imageWidth      = 1280
	headerHeight    = 60
	rowHeight       = 36
	footerHeight    = 50
	padding         = 20
	progressBarW    = 60
	progressBarH    = 16
	fontSize        = 14
	titleFontSize   = 24
	footerFontSize  = 12
	refreshInterval = 2 * time.Second
)

// 语言包
type langPack struct {
	Server          string
	Network         string
	Online          string
	Location        string
	Virtualization  string
	Load            string
	Traffic         string
	Speed           string
	CPU             string
	Memory          string
	Disk            string
	Price           string
	Remaining       string
	Offline         string
	DualStack       string
	Day             string
	Days            string
	Hour            string
	Hours           string
	Minute          string
	Minutes         string
	Year            string
	YearPlus        string
	Month           string
	Quarter         string
	HalfYear        string
	Free            string
	FreeOneTime     string
	LongTerm        string
	Expired         string
	LastUpdate      string
	PoweredBy       string
	PreparingMJPEG  string
	DownloadingFont string
	SpeedRemaining  string
	FontLoadFailed  string
	SaveFontTo      string
}

var langEN = langPack{
	Server:          "Server",
	Network:         "Network",
	Online:          "Uptime",
	Location:        "Loc",
	Virtualization:  "Virt",
	Load:            "Load",
	Traffic:         "Traffic",
	Speed:           "Speed",
	CPU:             "CPU",
	Memory:          "Mem",
	Disk:            "Disk",
	Price:           "Price",
	Remaining:       "Expire",
	Offline:         "Offline",
	DualStack:       "Dual",
	Day:             "d",
	Days:            "d",
	Hour:            "h",
	Hours:           "h",
	Minute:          "m",
	Minutes:         "m",
	Year:            "y",
	YearPlus:        "y+",
	Month:           "mo",
	Quarter:         "q",
	HalfYear:        "6mo",
	Free:            "Free",
	FreeOneTime:     "Free/Once",
	LongTerm:        "Long-term",
	Expired:         "Expired",
	LastUpdate:      "Last Update: ",
	PoweredBy:       "Powered by Komari Monitor",
	PreparingMJPEG:  "Preparing MJPEG Stream",
	DownloadingFont: "Downloading font %s / %s",
	SpeedRemaining:  "Speed %s, %ds remaining",
	FontLoadFailed:  "Failed to load font. Please manually download TTF file",
	SaveFontTo:      "and save to ./data/font.ttf",
}

var langZH = langPack{
	Server:          "服务器",
	Network:         "网络",
	Online:          "在线",
	Location:        "位置",
	Virtualization:  "虚拟化",
	Load:            "负载",
	Traffic:         "流量",
	Speed:           "网速",
	CPU:             "CPU",
	Memory:          "内存",
	Disk:            "磁盘",
	Price:           "价格",
	Remaining:       "剩余",
	Offline:         "离线",
	DualStack:       "双栈",
	Day:             "天",
	Days:            "天",
	Hour:            "时",
	Hours:           "时",
	Minute:          "分",
	Minutes:         "分",
	Year:            "年",
	YearPlus:        "年+",
	Month:           "月",
	Quarter:         "季",
	HalfYear:        "半年",
	Free:            "免费",
	FreeOneTime:     "免费/一次性",
	LongTerm:        "长期",
	Expired:         "已过期",
	LastUpdate:      "最后更新：",
	PoweredBy:       "Powered by Komari Monitor",
	PreparingMJPEG:  "Preparing MJPEG Stream",
	DownloadingFont: "Downloading font %s / %s",
	SpeedRemaining:  "Speed %s, %ds remaining",
	FontLoadFailed:  "Failed to load font. Please manually download TTF file",
	SaveFontTo:      "and save to ./data/font.ttf",
}

func getLangPack(lang string) langPack {
	switch strings.ToLower(lang) {
	case "zh_cn", "zh-cn", "zh":
		return langZH
	default:
		return langEN
	}
}

var (
	fontFace     font.Face
	fontFaceBold font.Face
	fontReady    bool
	fontError    error
	fontMutex    sync.RWMutex
	fontOnce     sync.Once

	// 基础字体（用于下载时显示）
	basicFace = basicfont.Face7x13

	// 字体下载状态
	downloadMutex    sync.Mutex
	downloadProgress *DownloadProgress
	downloading      bool
)

type DownloadProgress struct {
	Total      int64
	Downloaded int64
	Speed      float64 // bytes/s
	StartTime  time.Time
}

func (p *DownloadProgress) Percentage() float64 {
	if p.Total == 0 {
		return 0
	}
	return float64(p.Downloaded) / float64(p.Total) * 100
}

func (p *DownloadProgress) RemainingSeconds() int {
	if p.Speed <= 0 {
		return 0
	}
	remaining := float64(p.Total-p.Downloaded) / p.Speed
	return int(remaining)
}

// initFont 初始化字体（只执行一次）
func initFont() {
	fontOnce.Do(func() {
		loadFont()
	})
}

// loadFont 加载字体文件
func loadFont() {
	fontMutex.Lock()
	defer fontMutex.Unlock()

	// 检查字体文件是否存在
	if _, err := os.Stat(fontPath); err == nil {
		if err := loadFontFromFile(); err != nil {
			fontError = err
			return
		}
		fontReady = true
		return
	}

	// 尝试下载字体
	go downloadFont()
}

// loadFontFromFile 从文件加载字体
func loadFontFromFile() error {
	fontData, err := os.ReadFile(fontPath)
	if err != nil {
		fmt.Printf("[MJPEG] Failed to read font file: %v\n", err)
		return fmt.Errorf("failed to read font file: %w", err)
	}

	parsedFont, err := opentype.Parse(fontData)
	if err != nil {
		fmt.Printf("[MJPEG] Failed to parse font file (corrupted?): %v\n", err)
		return fmt.Errorf("failed to parse font: %w", err)
	}

	fontFace, err = opentype.NewFace(parsedFont, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		fmt.Printf("[MJPEG] Failed to create font face: %v\n", err)
		return fmt.Errorf("failed to create font face: %w", err)
	}

	fontFaceBold, err = opentype.NewFace(parsedFont, &opentype.FaceOptions{
		Size:    titleFontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		fmt.Printf("[MJPEG] Failed to create bold font face: %v\n", err)
		return fmt.Errorf("failed to create bold font face: %w", err)
	}

	return nil
}

// downloadFont 下载字体文件
func downloadFont() {
	downloadMutex.Lock()
	if downloading {
		downloadMutex.Unlock()
		// 等待下载完成
		for {
			downloadMutex.Lock()
			if !downloading {
				downloadMutex.Unlock()
				break
			}
			downloadMutex.Unlock()
			time.Sleep(100 * time.Millisecond)
		}
		return
	}
	downloading = true
	downloadProgress = &DownloadProgress{StartTime: time.Now()}
	downloadMutex.Unlock()

	defer func() {
		downloadMutex.Lock()
		downloading = false
		downloadProgress = nil
		downloadMutex.Unlock()
	}()

	// 创建目录
	if err := os.MkdirAll(filepath.Dir(fontPath), 0755); err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to create directory: %w", err)
		fontMutex.Unlock()
		return
	}

	// 下载 ZIP
	resp, err := http.Get(fontURL)
	if err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to download font: %w", err)
		fontMutex.Unlock()
		fmt.Printf("[MJPEG] Font download error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// 检查 HTTP 状态
	if resp.StatusCode != 200 {
		fontMutex.Lock()
		fontError = fmt.Errorf("font download failed with status %d", resp.StatusCode)
		fontMutex.Unlock()
		fmt.Printf("[MJPEG] Font download HTTP error: %d\n", resp.StatusCode)
		return
	}

	downloadMutex.Lock()
	downloadProgress.Total = resp.ContentLength
	downloadMutex.Unlock()

	// 读取并跟踪进度
	var buf bytes.Buffer
	lastUpdate := time.Now()
	lastBytes := int64(0)

	reader := &progressReader{
		reader: resp.Body,
		onProgress: func(n int64) {
			downloadMutex.Lock()
			defer downloadMutex.Unlock()
			downloadProgress.Downloaded += n

			// 计算速度（每秒更新一次）
			now := time.Now()
			if now.Sub(lastUpdate) >= time.Second {
				downloadProgress.Speed = float64(downloadProgress.Downloaded-lastBytes) / now.Sub(lastUpdate).Seconds()
				lastUpdate = now
				lastBytes = downloadProgress.Downloaded
			}
		},
	}

	if _, err := io.Copy(&buf, reader); err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to download font data: %w", err)
		fontMutex.Unlock()
		return
	}

	// 解压 ZIP 并提取 TTF
	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to open zip: %w", err)
		fontMutex.Unlock()
		fmt.Printf("[MJPEG] ZIP parsing error: %v\n", err)
		return
	}

	var fontFile *zip.File
	for _, f := range zipReader.File {
		if f.Name == fontZipEntry || strings.HasSuffix(f.Name, "MiSans-Normal.ttf") {
			fontFile = f
			break
		}
	}

	if fontFile == nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("font file not found in zip")
		fontMutex.Unlock()
		fmt.Printf("[MJPEG] Font file not found in ZIP archive\n")
		return
	}

	rc, err := fontFile.Open()
	if err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to open font in zip: %w", err)
		fontMutex.Unlock()
		return
	}
	defer rc.Close()

	fontData, err := io.ReadAll(rc)
	if err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to read font from zip: %w", err)
		fontMutex.Unlock()
		return
	}

	// 保存字体文件
	if err := os.WriteFile(fontPath, fontData, 0644); err != nil {
		fontMutex.Lock()
		fontError = fmt.Errorf("failed to save font file: %w", err)
		fontMutex.Unlock()
		return
	}

	// 加载字体
	fontMutex.Lock()
	defer fontMutex.Unlock()
	if err := loadFontFromFile(); err != nil {
		fontError = err
		fmt.Printf("[MJPEG] Failed to load font from file: %v\n", err)
		return
	}
	fontReady = true
	fontError = nil
	fmt.Printf("[MJPEG] Font loaded successfully\n")
}

type progressReader struct {
	reader     io.Reader
	onProgress func(int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.onProgress != nil {
		r.onProgress(int64(n))
	}
	return n, err
}

// MjpegLiveHandler 处理 MJPEG 流请求
func MjpegLiveHandler(c *gin.Context) {
	initFont()

	// 获取参数
	lang := c.DefaultQuery("lang", "en")
	tzOffsetStr := c.DefaultQuery("tz_offset", "")

	// 解析时区偏移
	var tzOffset *int
	if tzOffsetStr != "" {
		if offset, err := strconv.Atoi(tzOffsetStr); err == nil {
			tzOffset = &offset
		}
	}

	c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	ctx := c.Request.Context()
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	// 立即发送第一帧
	sendFrame(c.Writer, ctx, lang, tzOffset)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendFrame(c.Writer, ctx, lang, tzOffset)
		}
	}
}

func sendFrame(w http.ResponseWriter, ctx context.Context, lang string, tzOffset *int) {
	img := renderFrame(ctx, lang, tzOffset)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return
	}

	fmt.Fprintf(w, "--frame\r\n")
	fmt.Fprintf(w, "Content-Type: image/jpeg\r\n")
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", buf.Len())
	w.Write(buf.Bytes())
	fmt.Fprintf(w, "\r\n")

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func renderFrame(ctx context.Context, lang string, tzOffset *int) *image.RGBA {
	fontMutex.RLock()
	ready := fontReady
	ferr := fontError
	fontMutex.RUnlock()

	// 检查是否正在下载
	downloadMutex.Lock()
	isDownloading := downloading
	progress := downloadProgress
	downloadMutex.Unlock()

	if !ready {
		if isDownloading && progress != nil {
			return renderDownloadProgress(progress)
		}
		// 如果下载失败且不在下载中，尝试使用基础字体渲染
		if ferr != nil {
			return renderStatusTableWithBasicFont(ctx, lang, tzOffset, ferr)
		}
		return renderError(ferr)
	}

	return renderStatusTable(ctx, lang, tzOffset, ferr)
}

// renderDownloadProgress 渲染下载进度（使用基础字体）
func renderDownloadProgress(p *DownloadProgress) *image.RGBA {
	width := 600
	height := 200
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// 白色背景
	fillRect(img, 0, 0, width, height, color.White)

	// 使用基础字体渲染
	y := 60
	drawCenteredStringBasic(img, "Preparing MJPEG Stream", width/2, y, color.Black)

	y += 40
	downloaded := formatBytes(p.Downloaded)
	total := formatBytes(p.Total)
	drawCenteredStringBasic(img, fmt.Sprintf("Downloading font %s / %s", downloaded, total), width/2, y, color.Gray{128})

	y += 30
	speed := formatBytes(int64(p.Speed)) + "/s"
	remaining := p.RemainingSeconds()
	drawCenteredStringBasic(img, fmt.Sprintf("Speed %s, %ds remaining", speed, remaining), width/2, y, color.Gray{128})

	// 进度条
	barW := 400
	barH := 20
	barX := (width - barW) / 2
	barY := y + 20
	drawRect(img, barX, barY, barW, barH, color.Gray{200})
	fillW := int(float64(barW) * p.Percentage() / 100)
	if fillW > 0 {
		fillRect(img, barX, barY, fillW, barH, color.RGBA{66, 133, 244, 255})
	}

	return img
}

// renderError 渲染错误信息（使用基础字体）
func renderError(err error) *image.RGBA {
	width := 800
	height := 250
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	fillRect(img, 0, 0, width, height, color.White)

	y := 60
	drawCenteredStringBasic(img, "Preparing MJPEG Stream...", width/2, y, color.Black)

	if err != nil {
		y += 40
		drawCenteredStringBasic(img, "Error:", width/2, y, color.RGBA{200, 0, 0, 255})
		y += 25
		// 显示错误信息（截断过长的内容）
		errMsg := err.Error()
		if len(errMsg) > 80 {
			errMsg = errMsg[:77] + "..."
		}
		drawCenteredStringBasic(img, errMsg, width/2, y, color.RGBA{150, 0, 0, 255})
		y += 35
		drawCenteredStringBasic(img, "Please check ./data/font.ttf or server logs", width/2, y, color.Gray{100})
	}

	return img
}

// NodeInfo 节点信息结构
type NodeInfo struct {
	UUID           string
	Name           string
	IPv4           string
	IPv6           string
	Region         string
	Virtualization string
	Price          float64
	BillingCycle   int
	Currency       string
	ExpiredAt      time.Time
	AutoRenewal    bool
	Hidden         bool
	Weight         int
	MemTotal       int64
	DiskTotal      int64
}

// NodeStatus 节点状态结构
type NodeStatus struct {
	Online       bool
	Uptime       int64
	Load         float32
	CPU          float32
	Ram          int64
	RamTotal     int64
	Disk         int64
	DiskTotal    int64
	NetTotalUp   int64
	NetTotalDown int64
	NetIn        int64
	NetOut       int64
}

// renderStatusTable 渲染状态表格
func renderStatusTable(ctx context.Context, lang string, tzOffset *int, fontErr error) *image.RGBA {
	lp := getLangPack(lang)

	// 获取节点数据
	nodes, statuses, fetchErr := fetchNodeData(ctx)
	if fetchErr != nil {
		return renderError(fetchErr)
	}

	// 过滤隐藏节点并排序（weight 小号在前）
	var visibleNodes []NodeInfo
	for _, n := range nodes {
		if !n.Hidden {
			visibleNodes = append(visibleNodes, n)
		}
	}
	sort.Slice(visibleNodes, func(i, j int) bool {
		if visibleNodes[i].Weight != visibleNodes[j].Weight {
			return visibleNodes[i].Weight < visibleNodes[j].Weight
		}
		return visibleNodes[i].Name < visibleNodes[j].Name
	})

	// 计算图像高度
	numRows := len(visibleNodes)
	if numRows == 0 {
		numRows = 1
	}
	height := headerHeight + rowHeight + numRows*rowHeight + footerHeight + padding*2

	img := image.NewRGBA(image.Rect(0, 0, imageWidth, height))
	fillRect(img, 0, 0, imageWidth, height, color.White)

	y := padding

	// 标题
	siteName, _ := conf.GetAs[string](conf.SitenameKey, "Komari Monitor")
	fontMutex.RLock()
	boldFace := fontFaceBold
	normalFace := fontFace
	fontMutex.RUnlock()

	drawString(img, siteName, padding+5, y+titleFontSize, color.Black, boldFace)
	y += headerHeight

	// 表头
	headers := []string{lp.Server, lp.Network, lp.Online, lp.Location, lp.Virtualization, lp.Load, lp.Traffic, lp.Speed, lp.CPU, lp.Memory, lp.Disk, lp.Price, lp.Remaining}
	colWidths := []int{130, 70, 55, 60, 90, 50, 140, 140, 70, 70, 70, 110, 80}

	// 绘制表头背景
	fillRect(img, padding, y, imageWidth-padding*2, rowHeight, color.RGBA{245, 245, 245, 255})

	x := padding + 5
	for i, h := range headers {
		drawString(img, h, x, y+rowHeight/2+fontSize/3, color.RGBA{100, 100, 100, 255}, normalFace)
		x += colWidths[i]
	}
	y += rowHeight

	// 数据行
	for idx, node := range visibleNodes {
		// 交替背景色
		if idx%2 == 1 {
			fillRect(img, padding, y, imageWidth-padding*2, rowHeight, color.RGBA{250, 250, 250, 255})
		}

		status, hasStatus := statuses[node.UUID]
		x = padding + 5
		textY := y + rowHeight/2 + fontSize/3

		// 服务器名
		drawString(img, truncateString(node.Name, 14), x, textY, color.Black, normalFace)
		x += colWidths[0]

		// 网络
		network := getNetworkTypeL(node.IPv4, node.IPv6, lp)
		drawString(img, network, x, textY, color.Black, normalFace)
		x += colWidths[1]

		// 在线时间
		if hasStatus && status.Online {
			uptime := formatUptimeL(status.Uptime, lp)
			drawString(img, uptime, x, textY, color.RGBA{0, 150, 0, 255}, normalFace)
		} else {
			drawString(img, lp.Offline, x, textY, color.RGBA{200, 0, 0, 255}, normalFace)
		}
		x += colWidths[2]

		// 位置
		drawString(img, getRegionShort(node.Region), x, textY, color.Black, normalFace)
		x += colWidths[3]

		// 虚拟化
		drawString(img, truncateString(node.Virtualization, 12), x, textY, color.Black, normalFace)
		x += colWidths[4]

		if hasStatus && status.Online {
			// 负载
			drawString(img, fmt.Sprintf("%.2f", status.Load), x, textY, color.Black, normalFace)
			x += colWidths[5]

			// 流量
			traffic := fmt.Sprintf("↑%s/↓%s", formatBytes(status.NetTotalUp), formatBytes(status.NetTotalDown))
			drawString(img, traffic, x, textY, color.Black, normalFace)
			x += colWidths[6]

			// 网速
			speed := fmt.Sprintf("↑%s/↓%s", formatBytes(status.NetOut), formatBytes(status.NetIn))
			drawString(img, speed, x, textY, color.Black, normalFace)
			x += colWidths[7]

			// CPU 进度条
			drawProgressBar(img, x, y+(rowHeight-progressBarH)/2, progressBarW, progressBarH, float64(status.CPU), normalFace)
			x += colWidths[8]

			// 内存进度条
			memPct := 0.0
			if status.RamTotal > 0 {
				memPct = float64(status.Ram) / float64(status.RamTotal) * 100
			}
			drawProgressBar(img, x, y+(rowHeight-progressBarH)/2, progressBarW, progressBarH, memPct, normalFace)
			x += colWidths[9]

			// 磁盘进度条
			diskPct := 0.0
			if status.DiskTotal > 0 {
				diskPct = float64(status.Disk) / float64(status.DiskTotal) * 100
			}
			drawProgressBar(img, x, y+(rowHeight-progressBarH)/2, progressBarW, progressBarH, diskPct, normalFace)
			x += colWidths[10]
		} else {
			// 离线状态显示 -
			for i := 5; i < 11; i++ {
				drawString(img, "-", x, textY, color.Gray{150}, normalFace)
				x += colWidths[i]
			}
		}

		// 价格
		priceStr := formatPriceL(node.Price, node.BillingCycle, node.Currency, lp)
		drawString(img, priceStr, x, textY, color.Black, normalFace)
		x += colWidths[11]

		// 剩余时间
		remainStr := formatRemainingL(node.ExpiredAt, node.AutoRenewal, lp)
		drawString(img, remainStr, x, textY, color.Black, normalFace)

		y += rowHeight
	}

	// 页脚
	y += 10
	now := formatTimeWithOffset(time.Now(), tzOffset)
	drawCenteredString(img, lp.LastUpdate+now, imageWidth/2, y+footerFontSize, color.Gray{100}, normalFace)

	y += 25
	drawCenteredString(img, lp.PoweredBy, imageWidth/2, y+footerFontSize, color.Gray{150}, normalFace)

	// 如果字体加载失败，显示警告
	if fontErr != nil {
		y += 20
		drawCenteredString(img, "Warning: Failed to load custom font", imageWidth/2, y+footerFontSize, color.RGBA{200, 100, 0, 255}, normalFace)
	}

	return img
}

// renderStatusTableWithBasicFont 使用基础字体渲染状态表格（当自定义字体下载失败时）
func renderStatusTableWithBasicFont(ctx context.Context, lang string, tzOffset *int, fontErr error) *image.RGBA {
	lp := getLangPack("en") // 基础字体只支持英文

	// 获取节点数据
	nodes, statuses, fetchErr := fetchNodeData(ctx)
	if fetchErr != nil {
		return renderError(fetchErr)
	}

	// 过滤隐藏节点并排序（weight 小号在前）
	var visibleNodes []NodeInfo
	for _, n := range nodes {
		if !n.Hidden {
			visibleNodes = append(visibleNodes, n)
		}
	}
	sort.Slice(visibleNodes, func(i, j int) bool {
		if visibleNodes[i].Weight != visibleNodes[j].Weight {
			return visibleNodes[i].Weight < visibleNodes[j].Weight
		}
		return visibleNodes[i].Name < visibleNodes[j].Name
	})

	// 计算图像高度
	numRows := len(visibleNodes)
	if numRows == 0 {
		numRows = 1
	}
	height := headerHeight + rowHeight + numRows*rowHeight + footerHeight + padding*2 + 30

	img := image.NewRGBA(image.Rect(0, 0, imageWidth, height))
	fillRect(img, 0, 0, imageWidth, height, color.White)

	y := padding

	// 标题
	siteName, _ := conf.GetAs[string](conf.SitenameKey, "Komari Monitor")

	drawStringBasic(img, siteName, padding+5, y+titleFontSize, color.Black)
	y += headerHeight

	// 表头
	headers := []string{lp.Server, lp.Network, lp.Online, lp.Location, lp.Virtualization, lp.Load, lp.Traffic, lp.Speed, lp.CPU, lp.Memory, lp.Disk, lp.Price, lp.Remaining}
	colWidths := []int{130, 50, 55, 60, 70, 50, 120, 110, 70, 70, 70, 110, 80}

	// 绘制表头背景
	fillRect(img, padding, y, imageWidth-padding*2, rowHeight, color.RGBA{245, 245, 245, 255})

	x := padding + 5
	for i, h := range headers {
		drawStringBasic(img, h, x, y+rowHeight/2+fontSize/3, color.RGBA{100, 100, 100, 255})
		x += colWidths[i]
	}
	y += rowHeight

	// 数据行
	for idx, node := range visibleNodes {
		// 交替背景色
		if idx%2 == 1 {
			fillRect(img, padding, y, imageWidth-padding*2, rowHeight, color.RGBA{250, 250, 250, 255})
		}

		status, hasStatus := statuses[node.UUID]
		x = padding + 5
		textY := y + rowHeight/2 + fontSize/3

		// 服务器名
		drawStringBasic(img, truncateString(node.Name, 14), x, textY, color.Black)
		x += colWidths[0]

		// 网络
		network := getNetworkTypeL(node.IPv4, node.IPv6, lp)
		drawStringBasic(img, network, x, textY, color.Black)
		x += colWidths[1]

		// 在线时间
		if hasStatus && status.Online {
			uptime := formatUptimeL(status.Uptime, lp)
			drawStringBasic(img, uptime, x, textY, color.RGBA{0, 150, 0, 255})
		} else {
			drawStringBasic(img, lp.Offline, x, textY, color.RGBA{200, 0, 0, 255})
		}
		x += colWidths[2]

		// 位置
		drawStringBasic(img, getRegionShort(node.Region), x, textY, color.Black)
		x += colWidths[3]

		// 虚拟化
		drawStringBasic(img, truncateString(node.Virtualization, 8), x, textY, color.Black)
		x += colWidths[4]

		if hasStatus && status.Online {
			// 负载
			drawStringBasic(img, fmt.Sprintf("%.2f", status.Load), x, textY, color.Black)
			x += colWidths[5]

			// 流量
			traffic := fmt.Sprintf("u%s/d%s", formatBytes(status.NetTotalUp), formatBytes(status.NetTotalDown))
			drawStringBasic(img, traffic, x, textY, color.Black)
			x += colWidths[6]

			// 网速
			speed := fmt.Sprintf("u%s/d%s", formatBytes(status.NetOut), formatBytes(status.NetIn))
			drawStringBasic(img, speed, x, textY, color.Black)
			x += colWidths[7]

			// CPU 进度条
			drawProgressBarBasic(img, x, y+(rowHeight-progressBarH)/2, progressBarW, progressBarH, float64(status.CPU))
			x += colWidths[8]

			// 内存进度条
			memPct := 0.0
			if status.RamTotal > 0 {
				memPct = float64(status.Ram) / float64(status.RamTotal) * 100
			}
			drawProgressBarBasic(img, x, y+(rowHeight-progressBarH)/2, progressBarW, progressBarH, memPct)
			x += colWidths[9]

			// 磁盘进度条
			diskPct := 0.0
			if status.DiskTotal > 0 {
				diskPct = float64(status.Disk) / float64(status.DiskTotal) * 100
			}
			drawProgressBarBasic(img, x, y+(rowHeight-progressBarH)/2, progressBarW, progressBarH, diskPct)
			x += colWidths[10]
		} else {
			// 离线状态显示 -
			for i := 5; i < 11; i++ {
				drawStringBasic(img, "-", x, textY, color.Gray{150})
				x += colWidths[i]
			}
		}

		// 价格
		priceStr := formatPriceL(node.Price, node.BillingCycle, node.Currency, lp)
		drawStringBasic(img, priceStr, x, textY, color.Black)
		x += colWidths[11]

		// 剩余时间
		remainStr := formatRemainingL(node.ExpiredAt, node.AutoRenewal, lp)
		drawStringBasic(img, remainStr, x, textY, color.Black)

		y += rowHeight
	}

	// 页脚
	y += 10
	now := formatTimeWithOffset(time.Now(), tzOffset)
	drawCenteredStringBasic(img, lp.LastUpdate+now, imageWidth/2, y+footerFontSize, color.Gray{100})

	y += 25
	drawCenteredStringBasic(img, lp.PoweredBy, imageWidth/2, y+footerFontSize, color.Gray{150})

	// 显示字体加载失败警告
	y += 20
	drawCenteredStringBasic(img, "Warning: Failed to load custom font. Save TTF to ./data/font.ttf", imageWidth/2, y+footerFontSize, color.RGBA{200, 100, 0, 255})

	return img
}

// fetchNodeData 获取节点数据
func fetchNodeData(ctx context.Context) ([]NodeInfo, map[string]NodeStatus, error) {
	nodes := make([]NodeInfo, 0)
	statuses := make(map[string]NodeStatus)

	// 调用 common:getNodes (通过 api_rpc 进行权限控制)
	nodesResp := jsonRpc.OnInternalRequest(ctx, "guest", "common:getNodes", nil)
	if nodesResp != nil && nodesResp.Error != nil {
		// RPC 错误（包括私有站点拒绝访问）
		return nodes, statuses, fmt.Errorf("%v", nodesResp.Error.Message)
	}
	if nodesResp != nil && nodesResp.Result != nil {
		if nodesMap, ok := nodesResp.Result.(map[string]interface{}); ok {
			for _, v := range nodesMap {
				if nodeData, ok := v.(map[string]interface{}); ok {
					node := parseNodeInfo(nodeData)
					nodes = append(nodes, node)
				}
			}
		} else if nodesMap, ok := nodesResp.Result.(map[string]models.Client); ok {
			for _, client := range nodesMap {
				nodes = append(nodes, NodeInfo{
					UUID:           client.UUID,
					Name:           client.Name,
					IPv4:           client.IPv4,
					IPv6:           client.IPv6,
					Region:         client.Region,
					Virtualization: client.Virtualization,
					Price:          client.Price,
					BillingCycle:   client.BillingCycle,
					Currency:       client.Currency,
					ExpiredAt:      client.ExpiredAt.ToTime(),
					AutoRenewal:    client.AutoRenewal,
					Hidden:         client.Hidden,
					Weight:         client.Weight,
					MemTotal:       client.MemTotal,
					DiskTotal:      client.DiskTotal,
				})
			}
		}
	}

	// 调用 common:getNodesLatestStatus (通过 api_rpc 进行权限控制)
	statusResp := jsonRpc.OnInternalRequest(ctx, "guest", "common:getNodesLatestStatus", nil)
	if statusResp != nil && statusResp.Error != nil {
		// RPC 错误，返回错误以便在图片中显示
		return nodes, statuses, fmt.Errorf("%v", statusResp.Error.Message)
	}
	if statusResp != nil && statusResp.Result != nil {
		// 使用反射处理任意 map 类型，添加 panic recovery
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[MJPEG] Panic in status parsing: %v\n", r)
			}
		}()

		statusMap := statusResp.Result
		if m, ok := statusMap.(map[string]interface{}); ok {
			for uuid, v := range m {
				if statusData, ok := v.(map[string]interface{}); ok {
					statuses[uuid] = parseNodeStatus(statusData)
				}
			}
		} else {
			// 用反射处理其他 map 类型
			resultVal := reflect.ValueOf(statusMap)
			if resultVal.Kind() == reflect.Map {
				for _, key := range resultVal.MapKeys() {
					uuid := fmt.Sprint(key.Interface())
					value := resultVal.MapIndex(key)

					// 将值转换为 map[string]interface{}
					statusData := make(map[string]interface{})
					if value.Kind() == reflect.Struct {
						// 如果是结构体，使用反射获取字段
						valueType := value.Type()
						for i := 0; i < value.NumField(); i++ {
							field := valueType.Field(i)
							fieldVal := value.Field(i)
							jsonTag := field.Tag.Get("json")
							if jsonTag == "" {
								jsonTag = strings.ToLower(field.Name)
							}
							// 移除 json tag 中的选项（如 ,omitempty）
							jsonTag = strings.Split(jsonTag, ",")[0]

							// 处理类型转换
							switch v := fieldVal.Interface().(type) {
							case float32:
								statusData[jsonTag] = float64(v)
							case float64:
								statusData[jsonTag] = v
							default:
								statusData[jsonTag] = v
							}
						}
						statuses[uuid] = parseNodeStatus(statusData)
					} else if valueMap, ok := value.Interface().(map[string]interface{}); ok {
						statuses[uuid] = parseNodeStatus(valueMap)
					}
				}
			}
		}
	}
	return nodes, statuses, nil
}

func parseNodeInfo(data map[string]interface{}) NodeInfo {
	node := NodeInfo{}
	if v, ok := data["uuid"].(string); ok {
		node.UUID = v
	}
	if v, ok := data["name"].(string); ok {
		node.Name = v
	}
	if v, ok := data["ipv4"].(string); ok {
		node.IPv4 = v
	}
	if v, ok := data["ipv6"].(string); ok {
		node.IPv6 = v
	}
	if v, ok := data["region"].(string); ok {
		node.Region = v
	}
	if v, ok := data["virtualization"].(string); ok {
		node.Virtualization = v
	}
	if v, ok := data["price"].(float64); ok {
		node.Price = v
	}
	if v, ok := data["billing_cycle"].(float64); ok {
		node.BillingCycle = int(v)
	}
	if v, ok := data["currency"].(string); ok {
		node.Currency = v
	}
	if v, ok := data["auto_renewal"].(bool); ok {
		node.AutoRenewal = v
	}
	if v, ok := data["hidden"].(bool); ok {
		node.Hidden = v
	}
	if v, ok := data["weight"].(float64); ok {
		node.Weight = int(v)
	}
	if v, ok := data["mem_total"].(float64); ok {
		node.MemTotal = int64(v)
	}
	if v, ok := data["disk_total"].(float64); ok {
		node.DiskTotal = int64(v)
	}
	// 解析 expired_at
	if v, ok := data["expired_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			node.ExpiredAt = t
		}
	}
	return node
}

func parseNodeStatus(data map[string]interface{}) NodeStatus {
	status := NodeStatus{}
	if v, ok := data["online"].(bool); ok {
		status.Online = v
	}
	if v, ok := data["uptime"].(int64); ok {
		status.Uptime = int64(v)
	}
	if v, ok := data["load"].(float64); ok {
		status.Load = float32(v)
	}
	if v, ok := data["cpu"].(float64); ok {
		status.CPU = float32(v)
	}
	if v, ok := data["ram"].(int64); ok {
		status.Ram = int64(v)
	}
	if v, ok := data["ram_total"].(int64); ok {
		status.RamTotal = int64(v)
	}
	if v, ok := data["disk"].(int64); ok {
		status.Disk = int64(v)
	}
	if v, ok := data["disk_total"].(int64); ok {
		status.DiskTotal = int64(v)
	}
	if v, ok := data["net_total_up"].(int64); ok {
		status.NetTotalUp = int64(v)
	}
	if v, ok := data["net_total_down"].(int64); ok {
		status.NetTotalDown = int64(v)
	}
	if v, ok := data["net_in"].(int64); ok {
		status.NetIn = int64(v)
	}
	if v, ok := data["net_out"].(int64); ok {
		status.NetOut = int64(v)
	}
	return status
}

// 绘图辅助函数

func fillRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			img.Set(x+dx, y+dy, c)
		}
	}
}

func drawRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	for dx := 0; dx < w; dx++ {
		img.Set(x+dx, y, c)
		img.Set(x+dx, y+h-1, c)
	}
	for dy := 0; dy < h; dy++ {
		img.Set(x, y+dy, c)
		img.Set(x+w-1, y+dy, c)
	}
}

func drawProgressBar(img *image.RGBA, x, y, w, h int, pct float64, face font.Face) {
	// 背景
	fillRect(img, x, y, w, h, color.RGBA{230, 230, 230, 255})

	// 选择颜色
	var barColor color.RGBA
	if pct < 50 {
		barColor = color.RGBA{76, 175, 80, 255} // 绿色
	} else if pct < 80 {
		barColor = color.RGBA{255, 193, 7, 255} // 黄色
	} else {
		barColor = color.RGBA{244, 67, 54, 255} // 红色
	}

	// 进度
	fillW := int(float64(w) * pct / 100)
	if fillW > w {
		fillW = w
	}
	if fillW > 0 {
		fillRect(img, x, y, fillW, h, barColor)
	}

	// 百分比文字
	text := fmt.Sprintf("%.1f%%", pct)
	if face != nil {
		// 计算文字宽度并居中
		bounds, _ := font.BoundString(face, text)
		textW := (bounds.Max.X - bounds.Min.X).Ceil()
		textX := x + (w-textW)/2
		textY := y + h/2 + 4
		drawString(img, text, textX, textY, color.Black, face)
	}
}

func drawProgressBarBasic(img *image.RGBA, x, y, w, h int, pct float64) {
	// 背景
	fillRect(img, x, y, w, h, color.RGBA{230, 230, 230, 255})

	// 选择颜色
	var barColor color.RGBA
	if pct < 50 {
		barColor = color.RGBA{76, 175, 80, 255} // 绿色
	} else if pct < 80 {
		barColor = color.RGBA{255, 193, 7, 255} // 黄色
	} else {
		barColor = color.RGBA{244, 67, 54, 255} // 红色
	}

	// 进度
	fillW := int(float64(w) * pct / 100)
	if fillW > w {
		fillW = w
	}
	if fillW > 0 {
		fillRect(img, x, y, fillW, h, barColor)
	}

	// 百分比文字（使用基础字体）
	text := fmt.Sprintf("%.0f%%", pct)
	bounds, _ := font.BoundString(basicFace, text)
	textW := (bounds.Max.X - bounds.Min.X).Ceil()
	textX := x + (w-textW)/2
	textY := y + h/2 + 4
	drawStringBasic(img, text, textX, textY, color.Black)
}

func drawString(img *image.RGBA, s string, x, y int, c color.Color, face font.Face) {
	if face == nil {
		// 备用渲染（无字体时）
		drawStringBasic(img, s, x, y, c)
		return
	}

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(s)
}

func drawCenteredString(img *image.RGBA, s string, centerX, y int, c color.Color, face font.Face) {
	if face == nil {
		// 备用渲染
		drawCenteredStringBasic(img, s, centerX, y, c)
		return
	}

	bounds, _ := font.BoundString(face, s)
	textW := (bounds.Max.X - bounds.Min.X).Ceil()
	x := centerX - textW/2

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(s)
}

// drawStringBasic 使用基础字体渲染字符串
func drawStringBasic(img *image.RGBA, s string, x, y int, c color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: basicFace,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(s)
}

// drawCenteredStringBasic 使用基础字体居中渲染字符串
func drawCenteredStringBasic(img *image.RGBA, s string, centerX, y int, c color.Color) {
	bounds, _ := font.BoundString(basicFace, s)
	textW := (bounds.Max.X - bounds.Min.X).Ceil()
	x := centerX - textW/2

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: basicFace,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(s)
}

// drawBasicString 简单的字符串渲染（无字体时使用）- 保留用于兼容
func drawBasicString(img *image.RGBA, s string, x, y int, c color.Color) {
	drawStringBasic(img, s, x, y, c)
}

// 格式化辅助函数

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	val := float64(b) / float64(div)
	units := []string{"K", "M", "G", "T", "P"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	if val >= 100 {
		return fmt.Sprintf("%.0f%s", val, units[exp])
	} else if val >= 10 {
		return fmt.Sprintf("%.1f%s", val, units[exp])
	}
	return fmt.Sprintf("%.2f%s", val, units[exp])
}

func formatUptime(seconds int64) string {
	days := seconds / 86400
	if days > 0 {
		return fmt.Sprintf("%d天", days)
	}
	hours := seconds / 3600
	if hours > 0 {
		return fmt.Sprintf("%d时", hours)
	}
	minutes := seconds / 60
	return fmt.Sprintf("%d分", minutes)
}

func formatUptimeL(seconds int64, lp langPack) string {
	days := seconds / 86400
	if days > 0 {
		return fmt.Sprintf("%d%s", days, lp.Days)
	}
	hours := seconds / 3600
	if hours > 0 {
		return fmt.Sprintf("%d%s", hours, lp.Hours)
	}
	minutes := seconds / 60
	return fmt.Sprintf("%d%s", minutes, lp.Minutes)
}

func formatPrice(price float64, cycle int, currency string) string {
	if price < 0 {
		return "免费/一次性"
	}
	if price == 0 {
		return "免费"
	}

	if currency == "" {
		currency = "$"
	}

	var cycleStr string
	switch cycle {
	case 30:
		cycleStr = "月"
	case 90:
		cycleStr = "季"
	case 180:
		cycleStr = "半年"
	case 360, 365:
		cycleStr = "年"
	case -1:
		cycleStr = "年"
	default:
		if cycle > 0 {
			cycleStr = fmt.Sprintf("%d天", cycle)
		} else {
			cycleStr = "年"
		}
	}

	// 如果是整数，不显示小数点
	if price == float64(int(price)) {
		return fmt.Sprintf("%s%d/%s", currency, int(price), cycleStr)
	}
	return fmt.Sprintf("%s%.2f/%s", currency, price, cycleStr)
}

func formatPriceL(price float64, cycle int, currency string, lp langPack) string {
	if price < 0 {
		return lp.FreeOneTime
	}
	if price == 0 {
		return lp.Free
	}

	if currency == "" {
		currency = "$"
	}

	var cycleStr string
	switch cycle {
	case 30:
		cycleStr = lp.Month
	case 90:
		cycleStr = lp.Quarter
	case 180:
		cycleStr = lp.HalfYear
	case 360, 365:
		cycleStr = lp.Year
	case -1:
		cycleStr = lp.Year
	default:
		if cycle > 0 {
			cycleStr = fmt.Sprintf("%d%s", cycle, lp.Days)
		} else {
			cycleStr = lp.Year
		}
	}

	// 如果是整数，不显示小数点
	if price == float64(int(price)) {
		return fmt.Sprintf("%s%d/%s", currency, int(price), cycleStr)
	}
	return fmt.Sprintf("%s%.2f/%s", currency, price, cycleStr)
}

func formatRemaining(expiredAt time.Time, autoRenewal bool) string {
	if autoRenewal {
		return "长期"
	}

	now := time.Now()
	if expiredAt.IsZero() || expiredAt.Year() > 2200 {
		return "长期"
	}

	diff := expiredAt.Sub(now)
	if diff <= 0 {
		return "已过期"
	}

	days := int(diff.Hours() / 24)
	if days > 365 {
		years := days / 365
		return fmt.Sprintf("%d年+", years)
	}
	return fmt.Sprintf("%d天", days)
}

func formatRemainingL(expiredAt time.Time, autoRenewal bool, lp langPack) string {
	if autoRenewal {
		return lp.LongTerm
	}

	now := time.Now()
	if expiredAt.IsZero() || expiredAt.Year() > 2200 {
		return lp.LongTerm
	}

	diff := expiredAt.Sub(now)
	if diff <= 0 {
		return lp.Expired
	}

	days := int(diff.Hours() / 24)
	if days > 365 {
		years := days / 365
		return fmt.Sprintf("%d%s", years, lp.YearPlus)
	}
	return fmt.Sprintf("%d%s", days, lp.Days)
}

func getNetworkType(ipv4, ipv6 string) string {
	hasV4 := ipv4 != ""
	hasV6 := ipv6 != ""

	if hasV4 && hasV6 {
		return "双栈"
	} else if hasV4 {
		return "IPv4"
	} else if hasV6 {
		return "IPv6"
	}
	return "-"
}

func getNetworkTypeL(ipv4, ipv6 string, lp langPack) string {
	hasV4 := ipv4 != ""
	hasV6 := ipv6 != ""

	if hasV4 && hasV6 {
		return lp.DualStack
	} else if hasV4 {
		return "IPv4"
	} else if hasV6 {
		return "IPv6"
	}
	return "-"
}

// formatTimeWithOffset 根据时区偏移格式化时间
func formatTimeWithOffset(t time.Time, tzOffset *int) string {
	var loc *time.Location
	var tzName string

	if tzOffset != nil {
		offset := *tzOffset
		loc = time.FixedZone(fmt.Sprintf("UTC%+d", offset), offset*3600)
		if offset >= 0 {
			tzName = fmt.Sprintf("GMT+%d", offset)
		} else {
			tzName = fmt.Sprintf("GMT%d", offset)
		}
	} else {
		loc = t.Location()
		tzName = t.Format("MST")
	}

	return t.In(loc).Format("2006-01-02 15:04:05") + " " + tzName
}

func getRegionShort(region string) string {
	// 提取国旗 emoji 后的地区代码
	if len(region) == 0 {
		return "-"
	}

	// 常见国旗映射
	regionMap := map[string]string{
		"🇭🇰": "HK",
		"🇨🇳": "CN",
		"🇺🇸": "US",
		"🇯🇵": "JP",
		"🇸🇬": "SG",
		"🇰🇷": "KR",
		"🇬🇧": "UK",
		"🇩🇪": "DE",
		"🇫🇷": "FR",
		"🇳🇱": "NL",
		"🇦🇺": "AU",
		"🇨🇦": "CA",
		"🇷🇺": "RU",
		"🇮🇳": "IN",
		"🇹🇼": "TW",
	}

	for flag, code := range regionMap {
		if strings.Contains(region, flag) {
			return code
		}
	}

	// 如果是短字符串，直接返回
	if len(region) <= 4 {
		return region
	}

	// 截取前4个字符
	runes := []rune(region)
	if len(runes) > 4 {
		return string(runes[:4])
	}
	return region
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
