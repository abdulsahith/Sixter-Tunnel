; Sixter — Windows installer (Inno Setup 6)
; Builds sixter-setup.exe: installs the Sixter CLI + helpers and adds them to PATH,
; mirroring what the .deb does on Linux (/usr/local/bin/sixter*).
;
; Build with:  packaging\windows\build-windows-installer.ps1
; or directly:  ISCC.exe /DMyAppVersion=2.0.0 packaging\windows\sixter-setup.iss

#ifndef MyAppVersion
  #define MyAppVersion "2.0.0"
#endif

#define MyAppName "Sixter"
#define MyAppPublisher "Sixter"
#define MyAppURL "https://sixter.in"

[Setup]
; A stable AppId so upgrades replace the previous install (keep this GUID forever).
AppId={{8E2A7C41-6B2D-4F0E-9A33-0A1B2C3D4E5F}}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
DefaultDirName={autopf}\Sixter
DefaultGroupName=Sixter
DisableProgramGroupPage=yes
OutputDir=..\..\installer
OutputBaseFilename=sixter-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
; admin needed to write Program Files + the system PATH
PrivilegesRequired=admin
ChangesEnvironment=yes
UninstallDisplayName=Sixter CLI
; SetupIconFile=sixter.ico        ; uncomment once you add an .ico here

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "addtopath"; Description: "Add Sixter to the system PATH (recommended — lets you run 'sixter' from any terminal)"; GroupDescription: "Integration:"

[Files]
; client-side binaries an end user needs (built into ..\..\bin\windows by the build script)
Source: "..\..\bin\windows\sixter.exe";        DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\bin\windows\sixter-client.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\bin\windows\sixter-repo.exe";   DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\bin\windows\sixter-reposerver.exe";   DestDir: "{app}"; Flags: ignoreversion

[Icons]

Name: "{group}\Sixter Terminal"; Filename: "{cmd}"; Parameters: "/k title Sixter CLI && echo Sixter CLI ready.  Run:  sixter login"; WorkingDir: "{app}"
Name: "{group}\Sixter Documentation"; Filename: "{#MyAppURL}"
Name: "{group}\Uninstall Sixter"; Filename: "{uninstallexe}"

[Registry]
; append {app} to the system PATH (only when the task is selected and it's not already there)
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
  ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
  Check: NeedsAddPath('{app}'); Tasks: addtopath

[Run]
; run from {app} so 'sixter' resolves even before the new PATH is picked up
Filename: "{cmd}"; Parameters: "/k title Sixter CLI && sixter login"; WorkingDir: "{app}"; \
  Description: "Run 'sixter login' now"; Flags: postinstall skipifsilent unchecked

[Code]
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
  Needle: string;
begin
  if not RegQueryStringValue(HKEY_LOCAL_MACHINE,
    'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
    'Path', OrigPath) then
  begin
    Result := True;
    exit;
  end;
  Needle := ';' + ExpandConstant(Param) + ';';
  Result := Pos(LowerCase(Needle), ';' + LowerCase(OrigPath) + ';') = 0;
end;
