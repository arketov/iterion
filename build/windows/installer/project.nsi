# NSIS installer template for Iterion Desktop.
#
# This file is a starting point — Wails generates a full installer from
# its built-in template at build time when -nsis is passed. Override
# specific sections here as the project grows (e.g. file associations).

!include "wails_tools.nsh"

# Basic metadata. Names are intentionally English-only for v1; we'll add
# localisation when the product warrants it.
Name "Iterion"
# build/bin is where the release workflow looks for *-installer.exe.
OutFile "..\..\bin\iterion-desktop-${ARCH}-installer.exe"
InstallDir "$PROGRAMFILES64\Iterion"
RequestExecutionLevel admin

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "Iterion Desktop"
    SetOutPath "$INSTDIR"
    # Wails passes the freshly built desktop binary through ARG_WAILS_*_BINARY;
    # it is not copied next to this script.
    !ifdef ARG_WAILS_AMD64_BINARY
        File "/oname=iterion-desktop.exe" "${ARG_WAILS_AMD64_BINARY}"
    !else
        File "/oname=iterion-desktop.exe" "${ARG_WAILS_ARM64_BINARY}"
    !endif
    # Console CLI of the same version (task cli:build:windows:<arch>): the
    # desktop spawns it for the hidden __mcp-board / __mcp-ask-user servers.
    File "iterion.exe"
    CreateShortCut "$DESKTOP\Iterion.lnk" "$INSTDIR\iterion-desktop.exe"
    CreateShortCut "$SMPROGRAMS\Iterion.lnk" "$INSTDIR\iterion-desktop.exe"
    WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
    Delete "$INSTDIR\iterion-desktop.exe"
    Delete "$INSTDIR\iterion.exe"
    Delete "$INSTDIR\uninstall.exe"
    Delete "$DESKTOP\Iterion.lnk"
    Delete "$SMPROGRAMS\Iterion.lnk"
    RMDir "$INSTDIR"
SectionEnd
