package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/asd1asd00000/svm-panel/api"
	"github.com/asd1asd00000/svm-panel/database"
	"github.com/asd1asd00000/svm-panel/sshvpn"
)

const (
	Reset  = "\033[0m"
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Red    = "\033[31m"
	Purple = "\033[35m"
	Blue   = "\033[34m"
)

var iranTime = time.FixedZone("IRST", 12600)

func pause() {
	fmt.Println(Cyan + "\n[ Press ENTER to return to the Main Menu ]" + Reset)
	var dummy string
	_, _ = fmt.Scanln(&dummy)
}

func main() {
	mode := flag.String("mode", "panel", "Execution mode: panel, api, node")
	token := flag.String("token", "default_secret", "Security token")
	mainURL := flag.String("main-url", "", "Main server API url")
	flag.Parse()

	if *mode == "api" {
		database.ConnectDB()
		go sshvpn.StartSSHServer("0.0.0.0:2222", false, "", "")
		go database.StartAutoBackupDaemon()
		go database.StartLogMaintenanceDaemon() // استارت چرخه پاکسازی خودکار لاگ‌ها
		api.StartAPIServer(8080, *token)
		return
	}

	if *mode == "node" {
		if *mainURL == "" {
			os.Exit(1)
		}
		sshvpn.StartSSHServer("0.0.0.0:2222", true, *mainURL, *token)
		return
	}

	database.ConnectDB()
	defer database.DB.Close()

	for {
		autoBackupHours := database.GetSetting("auto_backup_hours")
		tgToken := database.GetSetting("tg_bot_token")
		tgChat := database.GetSetting("tg_chat_id")
		
		backupStatus := Red + "[ 🛡️ Auto-Backup: DISABLED ]" + Reset
		if autoBackupHours != "" && autoBackupHours != "0" && tgToken != "" && tgChat != "" {
			lastStatus := database.GetSetting("last_backup_status")
			if lastStatus == "FAILED" {
				backupStatus = Red + "[ 🛡️ Auto-Backup: ACTIVE | Last: FAILED (Check Logs) ]" + Reset
			} else if lastStatus == "SUCCESS" {
				backupStatus = Green + "[ 🛡️ Auto-Backup: ACTIVE | Last: SUCCESS ]" + Reset
			} else {
				backupStatus = Yellow + "[ 🛡️ Auto-Backup: ACTIVE | Last: PENDING ]" + Reset
			}
		}

		fmt.Print("\033[H\033[2J")
		fmt.Println(Cyan + "==============================================")
		fmt.Println("             ✨ SVM DISTRIBUTED PANEL ✨       ")
		fmt.Printf("      %s\n", backupStatus)
		fmt.Println("==============================================" + Reset)
		fmt.Println(Green + " 1)" + Reset + " Create User")
		fmt.Println(Yellow + " 2)" + Reset + " Manage Users")
		fmt.Println(Purple + " 3)" + Reset + " Panel Settings & Backup")
		fmt.Println(Cyan + " 4)" + Reset + " Connected Nodes Monitor (Analytics & Edit)")
		fmt.Println(Blue + " 5)" + Reset + " System Logs 📜")
		fmt.Println(Red + " 6)" + Reset + " Exit")
		fmt.Println(Cyan + "==============================================" + Reset)
		fmt.Print("Please select an option [1-6]: ")

		var choice int
		_, _ = fmt.Scanln(&choice)

		switch choice {
		case 1:
			reader := bufio.NewReader(os.Stdin)
			fmt.Println(Green + "\n--- Create New User ---" + Reset)
			
			fmt.Print("Username (3-15 chars): ")
			userStr, _ := reader.ReadString('\n')
			username := strings.TrimSpace(userStr)
			
			if username == "" {
				userStr, _ = reader.ReadString('\n')
				username = strings.TrimSpace(userStr)
			}

			if len(username) < 3 || len(username) > 15 {
				fmt.Println(Red + "❌ Error: Username must be 3-15 characters!" + Reset)
				pause()
				continue
			}

			fmt.Print("Password: ")
			passStr, _ := reader.ReadString('\n')
			password := strings.TrimSpace(passStr)

			fmt.Print("Validity (Days): ")
			daysStr, _ := reader.ReadString('\n')
			days, _ := strconv.Atoi(strings.TrimSpace(daysStr))

			fmt.Print("Traffic Limit (GB): ")
			volStr, _ := reader.ReadString('\n')
			volumeGB, _ := strconv.ParseFloat(strings.TrimSpace(volStr), 64)

			fmt.Print("UDPGW Port [Default 7301]: ")
			portStr, _ := reader.ReadString('\n')
			portStr = strings.TrimSpace(portStr)
			udpgwPort := 7301
			if portStr != "" {
				p, err := strconv.Atoi(portStr)
				if err == nil {
					udpgwPort = p
				}
			}

			subToken, err := database.CreateUser(username, password, days, volumeGB, udpgwPort)
			if err != nil {
				fmt.Println(Red+"❌ Error creating user:", err, Reset)
			} else {
				fmt.Println(Green+"✔️ User successfully created!"+Reset)
				panelURL := database.GetSetting("panel_url")
				fmt.Printf(Cyan+"🔗 Subscription Link:\n%s/sub/%s\n"+Reset, panelURL, subToken)
			}
			pause()

		case 2:
			fmt.Println(Yellow + "\n======================================================== USERS LIST ========================================================" + Reset)
			users, err := database.GetUsers()
			if err != nil || len(users) == 0 {
				fmt.Println(Red + "No users found or database error!" + Reset)
				pause()
				continue
			}
			
			liveOnlineMap := make(map[string]bool)
			client := http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get("http://127.0.0.1:8080/api/internal/online")
			if err == nil {
				var onlineUsers []string
				if json.NewDecoder(resp.Body).Decode(&onlineUsers) == nil {
					for _, u := range onlineUsers {
						liveOnlineMap[u] = true
					}
				}
				resp.Body.Close()
			}
			
			fmt.Printf(Blue+"%-4s | %-14s | %-9s | %-9s | %-16s | %-16s | %-10s | %-8s\n"+Reset, "Row", "Username", "Used", "Total", "Expiry", "Last Seen", "Account", "Status")
			fmt.Println(strings.Repeat("-", 124))
			
			for i, u := range users {
				usedMB := float64(u.DataUsed) / (1024 * 1024)
				totalGB := float64(u.DataLimit) / (1024 * 1024 * 1024)
				accountStatus := Green + "Active" + Reset
				if time.Now().After(u.ExpiryDate) || u.DataUsed >= u.DataLimit {
					accountStatus = Red + "Disabled" + Reset
				}
				
				connectionStatus := Red + "Offline" + Reset
				lastSeenStr := "Never"

				if liveOnlineMap[u.Username] {
					connectionStatus = Green + "Online" + Reset
					lastSeenStr = "Just Now"
				} else if u.LastSeen > 0 {
					lastSeenStr = time.Unix(u.LastSeen, 0).In(iranTime).Format("15:04 (2006/01/02)")
				}
				
				expiryStr := u.ExpiryDate.In(iranTime).Format("2006-01-02 15:04")
				fmt.Printf("%-4d | %-14s | %-6.2f MB | %-6.2f GB | %-16s | %-16s | %-19s | %s\n", i+1, u.Username, usedMB, totalGB, expiryStr, lastSeenStr, accountStatus, connectionStatus)
			}
			fmt.Println(Yellow + "============================================================================================================================" + Reset)

			fmt.Println("\n 1) Delete User\n 2) Add/Remove Days\n 3) Set New Traffic Limit\n 4) Reset Traffic\n 5) Reset Expiry Date\n 6) View Traffic Analytics\n 7) Show Subscription Link\n 0) Back")
			fmt.Print("Select option [0-7]: ")
			
			var subChoice int
			_, _ = fmt.Scanln(&subChoice)
			if subChoice == 0 { continue }

			fmt.Print("Enter Row Number: ")
			var rowNum int
			_, _ = fmt.Scanln(&rowNum)
			if rowNum < 1 || rowNum > len(users) {
				fmt.Println(Red + "❌ Invalid Row Number!" + Reset)
				pause(); continue
			}

			targetUsername := users[rowNum-1].Username
			targetToken := users[rowNum-1].SubToken
			
			switch subChoice {
			case 1:
				_ = database.DeleteUser(targetUsername)
				fmt.Println(Green+"✔️ User deleted."+Reset)
			case 2:
				var days int
				fmt.Print("Days to add/remove: ")
				_, _ = fmt.Scanln(&days)
				_ = database.UpdateUserExpiry(targetUsername, days)
				fmt.Println(Green+"✔️ Days successfully updated."+Reset)
			case 3:
				var gb float64
				fmt.Print("New Traffic Limit (GB): ")
				_, _ = fmt.Scanln(&gb)
				_ = database.UpdateUserDataLimit(targetUsername, gb)
				fmt.Println(Green+"✔️ Traffic limit successfully updated."+Reset)
			case 4:
				_ = database.ResetUserDataUsed(targetUsername)
				fmt.Println(Green+"✔️ Traffic successfully reset to 0."+Reset)
			case 5:
				var newDays int
				fmt.Print("New validity (Days from today): ")
				_, _ = fmt.Scanln(&newDays)
				_ = database.ResetUserExpiry(targetUsername, newDays)
				fmt.Println(Green+"✔️ Expiry date successfully reset."+Reset)
			case 6:
				stats := database.GetUserTrafficStats(targetUsername)
				fmt.Println(Cyan + "\n📊 Traffic Analytics for User: " + targetUsername + Reset)
				fmt.Println(strings.Repeat("-", 45))
				fmt.Printf(" %-20s | %-15s\n", "Period", "Data Consumed")
				fmt.Println(strings.Repeat("-", 45))
				fmt.Printf(" %-20s | %-.4f GB\n", "1 Hour Past", stats.H1)
				fmt.Printf(" %-20s | %-.4f GB\n", "6 Hours Past", stats.H6)
				fmt.Printf(" %-20s | %-.4f GB\n", "12 Hours Past", stats.H12)
				fmt.Printf(" %-20s | %-.4f GB\n", "24 Hours Past", stats.Today)
				fmt.Printf(" %-20s | %-.2f GB\n", "Last 7 Days", stats.ThisWeek)
				fmt.Printf(" %-20s | %-.2f GB\n", "Last 30 Days", stats.ThisMonth)
				fmt.Printf(" %-20s | %-.2f GB\n", "Last 3 Months (90D)", stats.M3)
				fmt.Println(strings.Repeat("-", 45))
			case 7:
				panelURL := database.GetSetting("panel_url")
				fmt.Printf(Cyan+"\n🔗 Subscription Link: %s/sub/%s\n"+Reset, panelURL, targetToken)
			}
			pause()

		case 3:
			fmt.Println(Purple + "\n--- Panel Settings & Backup Menu ---" + Reset)
			fmt.Println(" 1) Create Backup Now")
			fmt.Println(" 2) Restore from Backup Zip File")
			fmt.Println(" 3) Configure Telegram Bot & Auto-Backup")
			fmt.Println(" 4) Set Webpage Links (Announcements / Tutorials)")
			fmt.Println(" 0) Back")
			fmt.Print("Select option: ")
			var bkChoice int
			_, _ = fmt.Scanln(&bkChoice)
			switch bkChoice {
			case 1:
				var manualPass string
				fmt.Print("Enter zip password (ENTER for none): ")
				_, _ = fmt.Scanln(&manualPass)
				_ = database.RunAdvancedBackup(manualPass)
			case 2:
				var path, pass string
				fmt.Print("Enter zip path: ")
				_, _ = fmt.Scanln(&path)
				fmt.Print("Enter password: ")
				_, _ = fmt.Scanln(&pass)
				_ = database.RestoreBackup(path, pass)
			case 3:
				var token, chatID, pass, hours string
				fmt.Print("Telegram Bot Token: ")
				_, _ = fmt.Scanln(&token)
				if token != "" { _ = database.SetSetting("tg_bot_token", token) }
				fmt.Print("Telegram Chat ID: ")
				_, _ = fmt.Scanln(&chatID)
				if chatID != "" { _ = database.SetSetting("tg_chat_id", chatID) }
				fmt.Print("Auto-Backup Zip Password: ")
				_, _ = fmt.Scanln(&pass)
				if pass != "" { _ = database.SetSetting("zip_password", pass) }
				fmt.Print("Auto-Backup Interval (Hours): ")
				_, _ = fmt.Scanln(&hours)
				if hours != "" { _ = database.SetSetting("auto_backup_hours", hours) }
			case 4:
				reader := bufio.NewReader(os.Stdin)
				fmt.Println(Yellow + "\n[ Leave blank and press enter to keep current setting ]" + Reset)
				
				fmt.Printf("Announcement URL [Current: %s]: ", database.GetSetting("announcement_url"))
				annStr, _ := reader.ReadString('\n')
				annStr = strings.TrimSpace(annStr)
				if annStr != "" { _ = database.SetSetting("announcement_url", annStr) }
				
				fmt.Printf("Tutorial URL [Current: %s]: ", database.GetSetting("tutorial_url"))
				tutStr, _ := reader.ReadString('\n')
				tutStr = strings.TrimSpace(tutStr)
				if tutStr != "" { _ = database.SetSetting("tutorial_url", tutStr) }
				
				fmt.Println(Green + "✔️ Links updated successfully!" + Reset)
			}
			pause()
			
		case 4:
			fmt.Println(Cyan + "\n================================= CONNECTED NODES =================================" + Reset)
			nodes, err := database.GetNodes()
			if err != nil || len(nodes) == 0 {
				fmt.Println(Red + "No nodes found in the database." + Reset)
				pause(); continue
			}
			
			fmt.Printf(Blue+"%-4s | %-16s | %-15s | %-10s | %-20s\n"+Reset, "Row", "Node IP / Name", "Total Traffic", "Status", "Last Seen")
			fmt.Println(strings.Repeat("-", 76))
			
			for i, n := range nodes {
				trafficGB := float64(n.TotalTraffic) / (1024 * 1024 * 1024)
				status := Red + "Offline" + Reset
				
				if time.Now().Unix()-n.LastSeen < 120 { 
					status = Green + "Online" + Reset 
				}
				
				lastSeenStr := "Never"
				if n.LastSeen > 0 {
					lastSeenStr = time.Unix(n.LastSeen, 0).In(iranTime).Format("2006-01-02 15:04:05")
				}
				
				displayName := n.IP
				if n.CustomRemark != "" {
					displayName = n.CustomRemark + " (" + n.IP + ")"
				}
				fmt.Printf("%-4d | %-16s | %-12.2f GB | %-19s | %-20s\n", i+1, displayName, trafficGB, status, lastSeenStr)
			}
			fmt.Println(Cyan + "===================================================================================" + Reset)

			fmt.Println("\n 1) View Traffic Analytics\n 2) Edit Node Settings (Domain / Custom Remark)\n 0) Back")
			fmt.Print("Select option [0-2]: ")
			var nodeMenuChoice int
			_, _ = fmt.Scanln(&nodeMenuChoice)
			if nodeMenuChoice == 0 { continue }

			fmt.Print("Enter Row Number: ")
			var nodeRowNum int
			_, _ = fmt.Scanln(&nodeRowNum)
			if nodeRowNum < 1 || nodeRowNum > len(nodes) {
				fmt.Println(Red + "❌ Invalid Row Number!" + Reset)
				pause(); continue
			}
			targetNode := nodes[nodeRowNum-1]

			if nodeMenuChoice == 1 {
				stats := database.GetNodeTrafficStats(targetNode.IP)
				fmt.Println(Cyan + "\n📊 Traffic Analytics for Server: " + targetNode.IP + Reset)
				fmt.Println(strings.Repeat("-", 45))
				fmt.Printf(" %-20s | %-15s\n", "Period", "Data Processed")
				fmt.Println(strings.Repeat("-", 45))
				fmt.Printf(" %-20s | %-.4f GB\n", "1 Hour Past", stats.H1)
				fmt.Printf(" %-20s | %-.4f GB\n", "6 Hours Past", stats.H6)
				fmt.Printf(" %-20s | %-.4f GB\n", "12 Hours Past", stats.H12)
				fmt.Printf(" %-20s | %-.4f GB\n", "24 Hours Past", stats.Today)
				fmt.Printf(" %-20s | %-.2f GB\n", "Last 7 Days", stats.ThisWeek)
				fmt.Printf(" %-20s | %-.2f GB\n", "Last 30 Days", stats.ThisMonth)
				fmt.Printf(" %-20s | %-.2f GB\n", "Last 3 Months (90D)", stats.M3)
				fmt.Println(strings.Repeat("-", 45))
			} else if nodeMenuChoice == 2 {
				reader := bufio.NewReader(os.Stdin)
				fmt.Println(Yellow + "\n[ Leave blank and press ENTER to keep current configuration ]" + Reset)
				
				fmt.Printf("Node Domain / DDNS (e.g. de.domain.com) [Current: %s]: ", targetNode.Domain)
				domainStr, _ := reader.ReadString('\n')
				domainStr = strings.TrimSpace(domainStr)
				if domainStr == "" { domainStr = targetNode.Domain }

				fmt.Printf("Custom Remark Title (e.g. 🇫🇷 France S1) [Current: %s]: ", targetNode.CustomRemark)
				remarkStr, _ := reader.ReadString('\n')
				remarkStr = strings.TrimSpace(remarkStr)
				if remarkStr == "" { remarkStr = targetNode.CustomRemark }

				err := database.UpdateNodeSettings(targetNode.IP, domainStr, remarkStr)
				if err != nil {
					fmt.Println(Red + "❌ Error updating node settings: " + err.Error() + Reset)
				} else {
					fmt.Println(Green + "✔️ Node settings successfully updated!" + Reset)
				}
			}
			pause()

		case 5:
			fmt.Println(Cyan + "\n--- 📜 System & Backup Logs ---" + Reset)
			content, err := os.ReadFile("/root/svm-panel/system.log")
			if err != nil {
				fmt.Println(Yellow + "No logs found yet. The daemon might not have executed any tasks." + Reset)
			} else {
				lines := strings.Split(string(content), "\n")
				start := 0
				if len(lines) > 20 {
					start = len(lines) - 20 // نمایش 20 رویداد آخر
				}
				for i := start; i < len(lines); i++ {
					line := strings.TrimSpace(lines[i])
					if line != "" {
						if strings.Contains(line, "SUCCESS") || strings.Contains(line, "INFO") {
							fmt.Println(Green + line + Reset)
						} else {
							fmt.Println(Red + line + Reset)
						}
					}
				}
			}
			pause()

		case 6:
			os.Exit(0)
		}
	}
}
