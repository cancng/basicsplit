@echo off
rem -H=windowsgui: konsol penceresi acilmaz, program sistem tepsisinde calisir
go build -ldflags "-H=windowsgui" -o basicsplit.exe .
