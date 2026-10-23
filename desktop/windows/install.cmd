@echo off
rem ติดตั้งเมนูคลิกขวา Explorer "ส่งไป Share Group" (HKCU ไม่ต้องเป็นผู้ดูแล)
cd /d "%~dp0"
if not exist "ShareGroup.exe" (
  echo ไม่พบ ShareGroup.exe ในโฟลเดอร์นี้ — รัน desktop/windows/build.sh ก่อน
  exit /b 1
)
if not exist "share.exe" (
  echo ไม่พบ share.exe ในโฟลเดอร์นี้ — ต้องอยู่โฟลเดอร์เดียวกับ ShareGroup.exe
  exit /b 1
)
ShareGroup.exe -install
