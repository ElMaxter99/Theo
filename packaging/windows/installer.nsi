; Theo for Windows: a per-user installer (no administrator).
;
;   makensis -DVERSION=0.11.0 -DSRC=<folder with theo.exe, theo-desktop.exe, LICENSE>
;            -DICON=<theo.ico> -DOUT=<setup.exe> packaging/windows/installer.nsi
;
; It installs into %LOCALAPPDATA%\Programs\Theo, which Theo itself can write to,
; so "Update now" in the app replaces the programs without asking for rights.
; /S installs silently; /D=<folder> (last argument) chooses the folder.
Unicode true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!ifndef VERSION
  !error "pass -DVERSION=X.Y.Z"
!endif
!ifndef SRC
  !error "pass -DSRC=<folder with the programs>"
!endif
!ifndef OUT
  !define OUT "theo-setup.exe"
!endif

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "StrFunc.nsh"
${StrLoc}
${UnStrRep}

Name "Theo ${VERSION}"
OutFile "${OUT}"
InstallDir "$LOCALAPPDATA\Programs\Theo"
InstallDirRegKey HKCU "Software\Theo" "InstallDir"
BrandingText "Theo ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "Theo"
VIAddVersionKey "FileDescription" "Theo installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "MIT License"

!ifdef ICON
  !define MUI_ICON "${ICON}"
  !define MUI_UNICON "${ICON}"
!endif
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\theo-desktop.exe"
!define MUI_FINISHPAGE_RUN_TEXT "$(RunTheo)"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${SRC}\LICENSE"
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Spanish"

LangString RunTheo ${LANG_ENGLISH} "Open Theo"
LangString RunTheo ${LANG_SPANISH} "Abrir Theo"
LangString SecApp ${LANG_ENGLISH} "Theo (required)"
LangString SecApp ${LANG_SPANISH} "Theo (necesario)"
LangString SecDesk ${LANG_ENGLISH} "Shortcut on the desktop"
LangString SecDesk ${LANG_SPANISH} "Acceso directo en el escritorio"
LangString SecPath ${LANG_ENGLISH} "Add the theo command to PATH"
LangString SecPath ${LANG_SPANISH} "Añadir la orden theo al PATH"
LangString DescApp ${LANG_ENGLISH} "The Theo app and the theo command line, installed for your user only. Theo updates itself from the app."
LangString DescApp ${LANG_SPANISH} "La app de Theo y la línea de órdenes theo, instaladas solo para tu usuario. Theo se actualiza desde la propia app."

Section "!$(SecApp)" SEC_APP
  SectionIn RO
  SetOutPath "$INSTDIR"
  ; A running Theo keeps its file: rename it out of the way, as the in-app update does.
  IfFileExists "$INSTDIR\theo-desktop.exe" 0 +3
    Delete "$INSTDIR\theo-desktop.exe.old"
    Rename "$INSTDIR\theo-desktop.exe" "$INSTDIR\theo-desktop.exe.old"
  IfFileExists "$INSTDIR\theo.exe" 0 +3
    Delete "$INSTDIR\theo.exe.old"
    Rename "$INSTDIR\theo.exe" "$INSTDIR\theo.exe.old"
  File "${SRC}\theo.exe"
  File "${SRC}\theo-desktop.exe"
  File "${SRC}\LICENSE"
  Delete "$INSTDIR\theo.exe.old"
  Delete "$INSTDIR\theo-desktop.exe.old"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateDirectory "$SMPROGRAMS\Theo"
  CreateShortcut "$SMPROGRAMS\Theo\Theo.lnk" "$INSTDIR\theo-desktop.exe"
  CreateShortcut "$SMPROGRAMS\Theo\Uninstall Theo.lnk" "$INSTDIR\uninstall.exe"

  WriteRegStr HKCU "Software\Theo" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "DisplayName" "Theo"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "Publisher" "ElMaxter99"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "DisplayIcon" "$INSTDIR\theo-desktop.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo" "NoRepair" 1
SectionEnd

Section /o "$(SecDesk)" SEC_DESK
  CreateShortcut "$DESKTOP\Theo.lnk" "$INSTDIR\theo-desktop.exe"
SectionEnd

Section "$(SecPath)" SEC_PATH
  ReadRegStr $0 HKCU "Environment" "Path"
  ${StrLoc} $1 ";$0;" ";$INSTDIR;" ">"
  ${If} $1 == ""
    ${If} $0 == ""
      StrCpy $0 "$INSTDIR"
    ${Else}
      StrCpy $0 "$0;$INSTDIR"
    ${EndIf}
    WriteRegExpandStr HKCU "Environment" "Path" "$0"
    SendMessage ${HWND_BROADCAST} ${WM_WININICHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${EndIf}
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_APP} "$(DescApp)"
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  ; Not deleted: your projects and Theo's data folder.
  Delete "$INSTDIR\theo.exe"
  Delete "$INSTDIR\theo-desktop.exe"
  Delete "$INSTDIR\theo.exe.old"
  Delete "$INSTDIR\theo-desktop.exe.old"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\Theo\Theo.lnk"
  Delete "$SMPROGRAMS\Theo\Uninstall Theo.lnk"
  RMDir "$SMPROGRAMS\Theo"
  Delete "$DESKTOP\Theo.lnk"

  ReadRegStr $0 HKCU "Environment" "Path"
  ${UnStrRep} $1 ";$0;" ";$INSTDIR;" ";"
  StrCpy $1 $1 -1 1 ; drop the sentinel semicolons added above
  ${If} $1 != $0
    WriteRegExpandStr HKCU "Environment" "Path" "$1"
    SendMessage ${HWND_BROADCAST} ${WM_WININICHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${EndIf}

  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Theo"
  DeleteRegKey HKCU "Software\Theo"
SectionEnd
