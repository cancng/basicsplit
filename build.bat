@echo off
rem -H=windowsgui: no console window; the program lives in the system tray
go build -ldflags "-H=windowsgui" -o basicsplit.exe .
