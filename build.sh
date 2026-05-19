#!/bin/bash

go build ./cmd/ollamabot
if [ $? -eq 0 ]; then
    echo "Build completed"
    ./ollamabot.exe
else
    echo "Build failed"
    read -p "Press Enter to continue..."
    exit 1
fi