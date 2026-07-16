#!/bin/bash

clear

echo "=========================================="
echo "       SVM Distributed Panel Installer    "
echo "=========================================="
echo "1) Install MAIN Server (Panel + DB + API)"
echo "2) Install NODE Server (SSH Node Only)"
echo "3) Clean Uninstall"
echo "=========================================="
read -p "Please select an option (1-3): " choice

case $choice in
    1)
        echo "Installing MAIN Server..."
        mkdir -p /etc/mysql
        dpkg --configure -a >/dev/null 2>&1
        apt --fix-broken install -y >/dev/null 2>&1
        apt update && apt upgrade -y
        apt install -y mariadb-server mariadb-client golang git wget curl zip unzip

        # درخواست دامنه و تنظیمات SSL
        echo "=========================================="
        read -p "Enter Domain/Subdomain for Subscription Link (Leave empty to use Server IP): " panel_domain
        
        if [ -z "$panel_domain" ]; then
            SERVER_IP=$(curl -s ifconfig.me)
            PANEL_URL="http://$SERVER_IP:8080"
            echo "No domain entered. Using IP: $PANEL_URL"
        else
            PANEL_URL="https://$panel_domain"
            echo "Domain entered. Installing Nginx & SSL for $panel_domain..."
            apt install -y nginx certbot python3-certbot-nginx
            rm -f /etc/nginx/sites-enabled/default
            
            cat <<EOF > /etc/nginx/sites-available/svm-panel
server {
    listen 80;
    server_name $panel_domain;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
    }
}
EOF
            ln -sf /etc/nginx/sites-available/svm-panel /etc/nginx/sites-enabled/
            systemctl restart nginx
            
            # دریافت خودکار SSL
            certbot --nginx -d $panel_domain --non-interactive --agree-tos -m admin@$panel_domain --redirect
        fi
        echo "=========================================="

        echo "Configuring MariaDB..."
        systemctl start mariadb
        systemctl enable mariadb

        mysql -e "CREATE DATABASE IF NOT EXISTS svm_db;"
        mysql -e "CREATE USER IF NOT EXISTS 'svm_user'@'localhost' IDENTIFIED BY 'svm_password';"
        mysql -e "GRANT ALL PRIVILEGES ON svm_db.* TO 'svm_user'@'localhost';"
        mysql -e "FLUSH PRIVILEGES;"

        mysql -e "USE svm_db; 
        CREATE TABLE IF NOT EXISTS users (
            id INT AUTO_INCREMENT PRIMARY KEY,
            username VARCHAR(50) NOT NULL UNIQUE,
            password VARCHAR(255) NOT NULL,
            expiry_date DATETIME NOT NULL,
            data_limit BIGINT NOT NULL, 
            data_used BIGINT DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS settings (
            key_name VARCHAR(50) PRIMARY KEY,
            key_value VARCHAR(255) NOT NULL
        );"

        # ذخیره URL نهایی در دیتابیس برای استفاده در Go
        mysql -e "USE svm_db; REPLACE INTO settings (key_name, key_value) VALUES ('panel_url', '$PANEL_URL');"

        rm -rf /root/svm-panel
        git clone https://github.com/asd1asd00000/svm-panel.git /root/svm-panel
        cd /root/svm-panel

        go mod tidy
        go build -o svm-panel main.go
        cp svm-panel /usr/local/bin/
        chmod +x /usr/local/bin/svm-panel

        read -p "Set a secure Security Token for Nodes: " secret_token
        
        cat <<EOF > /etc/systemd/system/svm-api.service
[Unit]
Description=SVM Panel API Service
After=network.target mariadb.service

[Service]
Type=simple
ExecStart=/usr/local/bin/svm-panel --mode api --token $secret_token
Restart=always

[Install]
WantedBy=multi-user.target
EOF

        systemctl daemon-reload
        systemctl enable svm-api.service
        systemctl start svm-api.service

        echo "=========================================="
        echo "✔️ MAIN Server successfully installed!"
        echo "🔗 Your Base URL is: $PANEL_URL"
        echo "Run 'svm-panel' anytime to open the management menu."
        echo "=========================================="
        ;;
        
    2)
        echo "Installing NODE Server..."
        apt update && apt upgrade -y
        apt install -y golang git wget curl

        read -p "Enter MAIN Server API URL (e.g., http://1.2.3.4:8080): " main_url
        read -p "Enter MAIN Server Security Token: " node_token

        rm -rf /root/svm-panel
        git clone https://github.com/asd1asd00000/svm-panel.git /root/svm-panel
        cd /root/svm-panel

        go mod tidy
        go build -o svm-panel main.go
        cp svm-panel /usr/local/bin/
        chmod +x /usr/local/bin/svm-panel

        cat <<EOF > /etc/systemd/system/svm-node.service
[Unit]
Description=SVM Node SSH Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/svm-panel --mode node --main-url $main_url --token $node_token
Restart=always

[Install]
WantedBy=multi-user.target
EOF

        systemctl daemon-reload
        systemctl enable svm-node.service
        systemctl start svm-node.service

        echo "=========================================="
        echo "✔️ NODE Server successfully installed and connected!"
        echo "=========================================="
        ;;
        
    3)
        echo "Uninstalling SVM components..."
        systemctl stop svm-api.service >/dev/null 2>&1
        systemctl disable svm-api.service >/dev/null 2>&1
        systemctl stop svm-node.service >/dev/null 2>&1
        systemctl disable svm-node.service >/dev/null 2>&1
        
        rm -f /etc/systemd/system/svm-api.service
        rm -f /etc/systemd/system/svm-node.service
        systemctl daemon-reload
        
        systemctl stop mariadb >/dev/null 2>&1
        apt purge -y mariadb-server mariadb-client mariadb-common golang zip unzip nginx certbot python3-certbot-nginx
        apt autoremove -y
        
        rm -rf /var/lib/mysql /etc/mysql /root/svm-panel /usr/local/bin/svm-panel /etc/nginx/sites-available/svm-panel /etc/nginx/sites-enabled/svm-panel
        echo "✔️ Clean uninstall finished."
        ;;
    *)
        echo "Invalid option."
        ;;
esac
