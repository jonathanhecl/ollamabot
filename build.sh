#!/bin/bash

BUILD_TIME=$(date -u +'%Y-%m-%dT%H:%M:%SZ')

go build -ldflags "-X 'main.buildTime=$BUILD_TIME'" ./cmd/ollamabot
if [ $? -eq 0 ]; then
    echo "Build completed"
    if [[ "$OSTYPE" == "msys" || "$OSTYPE" == "cygwin" || "$OSTYPE" == "win32" ]]; then
        ./ollamabot.exe
    else
        ./ollamabot
    fi
else
    echo "Build failed"
    read -p "Press Enter to continue..."
    exit 1
fi