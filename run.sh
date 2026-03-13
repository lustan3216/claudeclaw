#!/bin/bash
# goclaudeclaw 自動更新 watchdog
# auto_update=true (預設) 每次重啟前 git pull + rebuild
# 可透過 Telegram /set auto_update false 關閉
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

GOBIN=/data/go/go/bin/go
LOG=/tmp/goclaudeclaw.log
CONFIG="$SCRIPT_DIR/config.json"

# 從 config.json 讀取 auto_update，預設 true
get_auto_update() {
    python3 -c "
import json, sys
try:
    d = json.load(open('$CONFIG'))
    print(str(d.get('auto_update', True)).lower())
except:
    print('true')
"
}

while true; do
    AUTO=$(get_auto_update)
    if [ "$AUTO" = "true" ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] 自動更新已啟用，拉取最新代碼..." | tee -a "$LOG"
        git pull origin main 2>&1 | tee -a "$LOG"

        echo "[$(date '+%Y-%m-%d %H:%M:%S')] 編譯中..." | tee -a "$LOG"
        if $GOBIN build -o goclaudeclaw.new ./cmd/goclaudeclaw/ 2>&1 | tee -a "$LOG"; then
            mv goclaudeclaw.new goclaudeclaw
            echo "[$(date '+%Y-%m-%d %H:%M:%S')] 編譯成功" | tee -a "$LOG"
        else
            echo "[$(date '+%Y-%m-%d %H:%M:%S')] 編譯失敗，使用舊版本" | tee -a "$LOG"
        fi
    else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] 自動更新已停用，直接重啟..." | tee -a "$LOG"
    fi

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 啟動 goclaudeclaw..." | tee -a "$LOG"
    ./goclaudeclaw >> "$LOG" 2>&1
    EXIT_CODE=$?
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] goclaudeclaw 退出 (code=$EXIT_CODE)，3 秒後重啟..." | tee -a "$LOG"
    sleep 3
done
