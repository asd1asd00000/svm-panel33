package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/asd1asd00000/svm-panel/database"
	"github.com/asd1asd00000/svm-panel/sshvpn"
)

type UsagePayload struct {
	Username   string `json:"username"`
	BytesAdded int64  `json:"bytes_added"`
}

type NPVConfig struct {
	SSHConfigType       string `json:"sshConfigType"`
	Remarks             string `json:"remarks"`
	SSHHost             string `json:"sshHost"`
	SSHPort             int    `json:"sshPort"`
	SSHUsername         string `json:"sshUsername"`
	SSHPassword         string `json:"sshPassword"`
	UDPGWPort           int    `json:"udpgwPort"`
	UDPGWTransparentDNS bool   `json:"udpgwTransparentDNS"`
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func getCountryFlag(ip string) (string, string) {
	if ip == "Main-Server" || ip == "127.0.0.1" || strings.HasPrefix(ip, "192.168") || strings.HasPrefix(ip, "10.") {
		return "Local", "🌍"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/" + ip)
	if err != nil {
		return "Unknown", "🌍"
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var result map[string]interface{}
	_ = json.Unmarshal(body, &result)
	countryCode, ok := result["countryCode"].(string)
	if !ok {
		return "Unknown", "🌍"
	}
	countryName, _ := result["country"].(string)
	flag := ""
	if len(countryCode) == 2 {
		flag = string([]rune{rune(countryCode[0]) + 127397, rune(countryCode[1]) + 127397})
	}
	return countryName, flag
}

func StartAPIServer(port int, token string) {
	http.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		users, err := database.GetUsers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type SyncUser struct {
			Username   string `json:"username"`
			Password   string `json:"password"`
			DataLimit  int64  `json:"data_limit"`
			DataUsed   int64  `json:"data_used"`
			ExpiryUnix int64  `json:"expiry_unix"`
		}
		var syncList []SyncUser
		for _, u := range users {
			syncList = append(syncList, SyncUser{
				Username:   u.Username,
				Password:   u.Password,
				DataLimit:  u.DataLimit,
				DataUsed:   u.DataUsed,
				ExpiryUnix: u.ExpiryDate.Unix(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(syncList)
	})

	http.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Query().Get("token") != token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		var payload UsagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		clientIP := r.Header.Get("X-Real-IP")
		if clientIP == "" {
			clientIP = r.Header.Get("X-Forwarded-For")
		}
		if clientIP == "" {
			var err error
			clientIP, _, err = net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				clientIP = r.RemoteAddr
			}
		}
		clientIP = strings.TrimSpace(strings.Split(clientIP, ",")[0])

		err := database.IncrementUserDataUsed(payload.Username, payload.BytesAdded, clientIP)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = database.IncrementNodeTraffic(clientIP, payload.BytesAdded)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Success")
	})

	http.HandleFunc("/api/online", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Query().Get("token") != token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		clientIP := r.Header.Get("X-Real-IP")
		if clientIP == "" {
			clientIP = r.Header.Get("X-Forwarded-For")
		}
		if clientIP == "" {
			var err error
			clientIP, _, err = net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				clientIP = r.RemoteAddr
			}
		}
		clientIP = strings.TrimSpace(strings.Split(clientIP, ",")[0])

		now := time.Now().Unix()
		_ = database.UpdateNodeLastSeen(clientIP, now)

		var onlineUsers []string
		if err := json.NewDecoder(r.Body).Decode(&onlineUsers); err == nil {
			sshvpn.UpdateNodeOnlineStatus(onlineUsers)
			for _, u := range onlineUsers {
				_ = database.UpdateLastSeen(u, now)
				_ = database.IncrementUserDataUsed(u, 0, clientIP)
			}
		}
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/api/internal/online", func(w http.ResponseWriter, r *http.Request) {
		onlineUsers := sshvpn.GetOnlineUsersList()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(onlineUsers)
	})

	http.HandleFunc("/sub/", func(w http.ResponseWriter, r *http.Request) {
		subToken := strings.TrimPrefix(r.URL.Path, "/sub/")
		if subToken == "" {
			http.Error(w, "404 - لینک نامعتبر است", http.StatusNotFound)
			return
		}

		user, err := database.GetUserBySubToken(subToken)
		if err != nil {
			http.Error(w, "404 - کاربر یافت نشد", http.StatusNotFound)
			return
		}

		iranTime := time.FixedZone("IRST", 12600)
		expiry := user.ExpiryDate.In(iranTime)
		now := time.Now().In(iranTime)

		statusBadge := `<span style="background: #10B981; color: white; padding: 5px 12px; border-radius: 20px; font-size: 14px;">✔ فعال</span>`
		if now.After(expiry) || user.DataUsed >= user.DataLimit {
			statusBadge = `<span style="background: #EF4444; color: white; padding: 5px 12px; border-radius: 20px; font-size: 14px;">✖ غیرفعال</span>`
		}

		onlineStatusBadge := `<span style="color: #EF4444; font-weight: bold;">🔴 آفلاین</span>`
		if sshvpn.IsUserOnline(user.Username) {
			onlineStatusBadge = `<span style="color: #10B981; font-weight: bold;">🟢 آنلاین</span>`
		}

		lastSeenStr := "بدون اتصال قبلی"
		if user.LastSeen > 0 {
			lastSeenStr = time.Unix(user.LastSeen, 0).In(iranTime).Format("2006-01-02 15:04")
		}

		usedFmt := formatBytes(user.DataUsed)
		limitFmt := formatBytes(user.DataLimit)
		
		remBytes := user.DataLimit - user.DataUsed
		if remBytes < 0 { remBytes = 0 }
		remFmt := formatBytes(remBytes)

		percent := float64(user.DataUsed) / float64(user.DataLimit) * 100
		if percent > 100 { percent = 100 }
		
		barColor := "#10B981"
		if percent > 80 { barColor = "#EF4444" } else if percent > 50 { barColor = "#F59E0B" }

		expiryStr := expiry.Format("2006-01-02 15:04")
		serverHost := strings.Split(r.Host, ":")[0]

		generateNPV := func(remark, host string) string {
			cfg := NPVConfig{
				SSHConfigType:       "SSH-Direct",
				Remarks:             remark,
				SSHHost:             host,
				SSHPort:             2222,
				SSHUsername:         user.Username,
				SSHPassword:         user.Password,
				UDPGWPort:           user.UdpgwPort,
				UDPGWTransparentDNS: true,
			}
			b, _ := json.Marshal(cfg)
			return "npvt-ssh://" + base64.StdEncoding.EncodeToString(b)
		}

		var configHTML strings.Builder
		configHTML.WriteString(`<div class="info-box"><h3 style="margin-bottom:15px;">📋 کانفیگ‌های اتصال (NPV)</h3>`)

		nodes, _ := database.GetNodes()

		var mainCustom database.Node
		for _, n := range nodes {
			if n.IP == "Main-Server" {
				mainCustom = n
				break
			}
		}

		mainHost := serverHost
		if mainCustom.Domain != "" {
			mainHost = mainCustom.Domain
		}

		mainRemark := ""
		if mainCustom.CustomRemark != "" {
			mainRemark = mainCustom.CustomRemark
		} else {
			mainCountry, mainFlag := getCountryFlag(serverHost)
			mainRemark = fmt.Sprintf("%s %s S1", mainFlag, mainCountry)
		}

		mainCfg := generateNPV(mainRemark, mainHost)
		configHTML.WriteString(fmt.Sprintf(`
			<div class="config-item">
				<span>%s</span>
				<button class="config-btn" onclick="copySingle('%s')">کپی کانفیگ</button>
			</div>`, mainRemark, mainCfg))

		nodeIdx := 2 
		for _, n := range nodes {
			if n.IP != "Main-Server" && n.IP != "" && n.IP != "127.0.0.1" {
				hostToUse := n.IP
				if n.Domain != "" {
					hostToUse = n.Domain
				}

				nodeRemark := ""
				if n.CustomRemark != "" {
					nodeRemark = n.CustomRemark
				} else {
					nodeCountry, nodeFlag := getCountryFlag(n.IP)
					nodeRemark = fmt.Sprintf("%s %s S%d", nodeFlag, nodeCountry, nodeIdx)
				}
				
				nodeCfg := generateNPV(nodeRemark, hostToUse)
				configHTML.WriteString(fmt.Sprintf(`
					<div class="config-item">
						<span>%s</span>
						<button class="config-btn" onclick="copySingle('%s')">کپی کانفیگ</button>
					</div>`, nodeRemark, nodeCfg))
				nodeIdx++
			}
		}
		configHTML.WriteString(`</div>`)

		annURL := database.GetSetting("announcement_url")
		if annURL == "" { annURL = "#" }
		tutURL := database.GetSetting("tutorial_url")
		if tutURL == "" { tutURL = "#" }

		rows, queryErr := database.DB.Query("SELECT DISTINCT node_ip FROM traffic_logs WHERE username = ?", user.Username)
		var userNodes []string
		if queryErr == nil && rows != nil {
			for rows.Next() {
				var nip string
				if rows.Scan(&nip) == nil { 
					userNodes = append(userNodes, nip) 
				}
			}
			rows.Close()
		}

		if len(userNodes) == 0 {
			userNodes = append(userNodes, "Main-Server")
		}

		type ApexDataset struct {
			Name string    `json:"name"`
			Data []float64 `json:"data"`
		}

		var datasets []ApexDataset

		for _, nodeIP := range userNodes {
			labelName := nodeIP
			if nodeIP == "Main-Server" { 
				labelName = mainRemark
			} else {
				for _, n := range nodes {
					if n.IP == nodeIP && n.CustomRemark != "" {
						labelName = n.CustomRemark
						break
					}
				}
			}

			ds := ApexDataset{
				Name: labelName,
				Data: make([]float64, 7),
			}

			fetchBytesHourly := func(hours int) float64 {
				tStr := now.Add(time.Duration(-hours) * time.Hour).Format("2006-01-02 15:00:00")
				var b int64
				_ = database.DB.QueryRow("SELECT IFNULL(SUM(bytes_used), 0) FROM traffic_logs WHERE username = ? AND node_ip = ? AND TIMESTAMP(log_date, MAKETIME(log_hour, 0, 0)) >= ?", user.Username, nodeIP, tStr).Scan(&b)
				return float64(b) / (1024 * 1024 * 1024)
			}

			fetchBytesDaily := func(days, months int) float64 {
				tStr := now.AddDate(0, -months, -days).Format("2006-01-02")
				var b int64
				_ = database.DB.QueryRow("SELECT IFNULL(SUM(bytes_used), 0) FROM traffic_logs WHERE username = ? AND node_ip = ? AND log_date >= ?", user.Username, nodeIP, tStr).Scan(&b)
				return float64(b) / (1024 * 1024 * 1024)
			}

			ds.Data[0] = fetchBytesHourly(1)
			ds.Data[1] = fetchBytesHourly(6)
			ds.Data[2] = fetchBytesHourly(12)
			ds.Data[3] = fetchBytesHourly(24)
			ds.Data[4] = fetchBytesDaily(7, 0)
			ds.Data[5] = fetchBytesDaily(0, 1)
			ds.Data[6] = fetchBytesDaily(0, 3)

			datasets = append(datasets, ds)
		}

		datasetsBytes, _ := json.Marshal(datasets)

		html := fmt.Sprintf(`
		<!DOCTYPE html>
		<html lang="fa" dir="rtl">
		<head>
			<meta charset="UTF-8">
			<meta name="viewport" content="width=device-width, initial-scale=1.0">
			<title>وضعیت اشتراک | %s</title>
			<script src="https://cdn.jsdelivr.net/npm/apexcharts"></script>
			<style>
				body { font-family: Tahoma, Arial, sans-serif; background-color: #0F172A; color: #F3F4F6; margin: 0; padding: 20px; display: flex; justify-content: center; align-items: center; min-height: 100vh; }
				.card { background-color: #1E293B; border-radius: 16px; padding: 30px; width: 100%%; max-width: 500px; box-shadow: 0 10px 25px rgba(0,0,0,0.5); border: 1px solid #334155; }
				.header { display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid #334155; padding-bottom: 15px; margin-bottom: 20px; }
				.header h1 { margin: 0; font-size: 22px; color: #F8FAFC; }
				.header small { color: #94A3B8; font-size: 14px; }
				.row { display: flex; justify-content: space-between; margin-bottom: 15px; font-size: 15px; }
				.val { font-weight: bold; }
				.green { color: #10B981; } .red { color: #EF4444; }
				.progress-bg { background-color: #334155; height: 12px; border-radius: 6px; margin: 25px 0 10px 0; overflow: hidden; }
				.progress-bar { height: 100%%; background-color: %s; width: %.2f%%; transition: width 0.5s; }
				.center-text { text-align: center; color: #94A3B8; font-size: 13px; margin-bottom: 25px; }
				.btn-group { display: flex; gap: 10px; margin-bottom: 25px; }
				.btn { flex: 1; padding: 12px; background: transparent; border: 1px solid #3B82F6; color: #3B82F6; border-radius: 8px; cursor: pointer; text-align: center; text-decoration: none; font-size: 14px; transition: 0.2s; }
				.btn:hover { background: #3B82F6; color: white; }
				.btn-red { border-color: #EF4444; color: #EF4444; } .btn-red:hover { background: #EF4444; color: white; }
				.btn-green { border-color: #10B981; color: #10B981; } .btn-green:hover { background: #10B981; color: white; }
				.info-box { background: #0F172A; border-radius: 8px; padding: 15px; border: 1px solid #334155; margin-bottom: 20px;}
				.info-box h3 { margin-top:0; font-size: 15px; color: #F8FAFC; text-align: center; margin-bottom: 15px; }
				.config-item { display: flex; justify-content: space-between; align-items: center; background: #1E293B; padding: 12px; border-radius: 8px; border: 1px solid #334155; margin-bottom: 10px; }
				.config-item span { font-size: 14px; color: #E2E8F0; }
				.config-btn { background: #3B82F6; color: white; border: none; padding: 8px 15px; border-radius: 6px; cursor: pointer; font-size: 12px; font-family: Tahoma; transition: 0.2s; }
				.config-btn:hover { background: #2563EB; }
				
				.apexcharts-tooltip {
					background: #1E293B !important;
					border: 1px solid #334155 !important;
					box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.5) !important;
					color: #F8FAFC !important;
					font-family: Tahoma, sans-serif !important;
				}
				.apexcharts-tooltip-title {
					background: #0F172A !important;
					border-bottom: 1px solid #334155 !important;
					font-family: Tahoma, sans-serif !important;
					font-weight: bold !important;
				}
			</style>
		</head>
		<body>
			<div class="card">
				<div class="header">
					<div>
						<small>کاربر <b>%s</b> خوش آمدید:</small>
						<h1>وضعیت اشتراک شما:</h1>
					</div>
					<div>%s</div>
				</div>

				<div class="row"><span>وضعیت اتصال فعلی:</span> <span class="val">%s</span></div>
				<div class="row"><span>حجم کل دوره:</span> <span class="val">%s</span></div>
				<div class="row"><span>حجم مصرف شده:</span> <span class="val">%s</span></div>
				<div class="row"><span>ترافیک باقی‌مانده:</span> <span class="val green">%s</span></div>
				<div class="row"><span>تاریخ انقضای کلی:</span> <span class="val red">%s</span></div>
				<div class="row"><span>آخرین استفاده از شبکه:</span> <span class="val">%s</span></div>

				<div class="progress-bg"><div class="progress-bar"></div></div>
				<div class="center-text">شما %.1f%% از حجم کل را مصرف کرده‌اید.</div>

				<div class="btn-group">
					<a href="%s" class="btn btn-red" target="_blank">📢 اطلاعیه‌ها</a>
					<a href="%s" class="btn btn-green" target="_blank">📚 آموزش‌ها</a>
				</div>

				%s

				<div class="info-box" style="margin-top: 25px;">
					<h3>تفکیک مصرف بازه‌ای به نسبت سرورها (GB)</h3>
					<div id="apexTrafficChart"></div>
				</div>
			</div>

			<script>
				function copySingle(text) {
					navigator.clipboard.writeText(text).then(function() {
						alert("کانفیگ سرور مورد نظر با موفقیت کپی شد!\nمی‌توانید در نرم‌افزار NPV الصاق کنید.");
					});
				}

				var options = {
					series: %s,
					chart: {
						type: 'bar',
						height: 350,
						stacked: true,
						toolbar: { show: false },
						fontFamily: 'Tahoma, Arial, sans-serif',
						foreColor: '#94A3B8',
						background: 'transparent'
					},
					dataLabels: {
						enabled: false
					},
					plotOptions: {
						bar: {
							horizontal: false,
							borderRadius: 4,
							columnWidth: '50%%',
						},
					},
					xaxis: {
						categories: ['1 ساعت', '6 ساعت', '12 ساعت', '24 ساعت گذشته', '1 هفته گذشته', '1 ماه گذشته', '3 ماه'],
						axisBorder: { show: false },
						axisTicks: { show: false },
						labels: {
							style: { fontSize: '11px' }
						}
					},
					yaxis: {
						labels: {
							formatter: function (val) {
								return val.toFixed(2);
							}
						}
					},
					grid: {
						borderColor: '#334155',
						strokeDashArray: 4,
					},
					legend: {
						position: 'top',
						horizontalAlign: 'center',
						labels: { colors: '#E2E8F0' },
						markers: { radius: 12 }
					},
					theme: {
						mode: 'dark',
						palette: 'palette1'
					},
					colors: ['#3B82F6', '#10B981', '#F59E0B', '#EF4444', '#8B5CF6', '#EC4899', '#06B6D4'],
					tooltip: {
						y: {
							formatter: function (val) {
								return val.toFixed(3) + " GB"
							}
						}
					}
				};

				var chart = new ApexCharts(document.querySelector("#apexTrafficChart"), options);
				chart.render();
			</script>
		</body>
		</html>
		`, user.Username, barColor, percent, user.Username, statusBadge, onlineStatusBadge, limitFmt, usedFmt, remFmt, expiryStr, lastSeenStr, percent, annURL, tutURL, configHTML.String(), string(datasetsBytes))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	})

	log.Printf("Control Panel API Server listening on port %d...\n", port)
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(port), nil))
}
