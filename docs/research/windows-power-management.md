# Windows power management from a Go app (Vitals) — API fact sheet

## Summary
- Settings live in the active power scheme as per-setting **AC/DC value indexes** keyed by `{scheme, subgroup, setting}` GUIDs. Two equivalent writers: `powercfg.exe` (needs a hidden console child) or `powrprof.dll` `PowerWrite{AC,DC}ValueIndex` + **mandatory** `PowerSetActiveScheme` (`powercfg /setactive SCHEME_CURRENT`) to make the change take effect.
- Standard users can edit scheme values and switch schemes (Vista+ UAC design); `powercfg /h on|off` (hiberfile) needs elevation → relaunch a tiny helper with `ShellExecuteExW` verb `runas`, user decline = `ERROR_CANCELLED` (1223).
- Immediate actions: `SetSuspendState` (needs `SeShutdownPrivilege` enabled; **fails on Modern Standby boxes**), `LockWorkStation`, `WM_SYSCOMMAND/SC_MONITORPOWER` broadcast, `ExitWindowsEx`/`InitiateSystemShutdownExW` (+`SeShutdownPrivilege`), `shutdown.exe /s /t N`, `schtasks` for recurring.
- Keep-awake: `SetThreadExecutionState` is **per-thread** (Go: `runtime.LockOSThread`); `PowerCreateRequest/PowerSetRequest` is process-scoped and visible in `powercfg /requests` — prefer it. Neither blocks lid-close/power-button sleep.
- AC/lid state: `GetSystemPowerStatus` (12-byte struct), `GetPwrCapabilities` (`LidPresent`, `AoAc`=Modern Standby, `HiberFilePresent`), `PowerSettingRegisterNotification(GUID_LIDSWITCH_STATE_CHANGE, DEVICE_NOTIFY_CALLBACK=2)` (no window needed; initial callback delivered immediately).

Verification note: `learn.microsoft.com` was unreachable from this sandbox (egress-blocked). API text below was verified against the **source markdown of those pages** in `MicrosoftDocs/sdk-api`, `MicrosoftDocs/win32`, `MicrosoftDocs/windowsserverdocs` on GitHub, and constants/signatures against the real SDK headers in `microsoft/win32metadata` (`RecompiledIdlHeaders/um/*.h`) plus mingw-w64 headers. The `powercfg` reference page itself (`windows-hardware/design/device-experiences/powercfg-command-line-options`) has no GitHub mirror; its contents were confirmed only through search-engine snippets — see Unverified.

## 1. GUIDs (WinNT.h, `DEFINE_GUID`) and `powercfg /aliases` names

| Alias | Symbol | GUID | Values |
|---|---|---|---|
| `SCHEME_CURRENT` | (alias only; resolves to `PowerGetActiveScheme`) | — | — |
| `SCHEME_BALANCED` | `GUID_TYPICAL_POWER_SAVINGS` | `381b4222-f694-41f0-9685-ff5bb260df2e` | Balanced |
| `SCHEME_MIN` | `GUID_MIN_POWER_SAVINGS` | `8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c` | High performance |
| `SCHEME_MAX` | `GUID_MAX_POWER_SAVINGS` | `a1841308-3541-4fab-bc81-f71556f20b4a` | Power saver |
| `SUB_NONE` | `NO_SUBGROUP_GUID` | `fea3413e-7e05-4911-9a71-700331f1c294` | "default" subgroup (e.g. `CONSOLELOCK` = `GUID_LOCK_CONSOLE_ON_WAKE` `0e796bdb-100d-47d6-a2d5-f7d2daa51f51`, 0/1) |
| `SUB_BUTTONS` | `GUID_SYSTEM_BUTTON_SUBGROUP` | `4f971e89-eebd-4455-a8de-9e59040e7347` | subgroup |
| `LIDACTION` | `GUID_LIDCLOSE_ACTION` | `5ca83367-6e45-459f-a27b-476b1d01c936` | 0 Do nothing, 1 Sleep, 2 Hibernate, 3 Shut down (no index 4 for the lid) |
| `PBUTTONACTION` | `GUID_POWERBUTTON_ACTION` | `7648efa3-dd9c-4e3e-b566-50f929386280` | 0 Do nothing, 1 Sleep, 2 Hibernate, 3 Shut down, 4 Turn off the display |
| `SBUTTONACTION` | `GUID_SLEEPBUTTON_ACTION` | `96996bc0-ad50-47ec-923b-6f41874dd9eb` | 0–4 as power button |
| `UIBUTTON_ACTION` | `GUID_USERINTERFACEBUTTON_ACTION` | `a7066653-8d6c-40a8-910e-a1f54b84c7e5` | Start-menu power button: 1 Sleep, 2 Hibernate, 3 Shut down |
| `LIDOPENWAKE` | `GUID_LIDOPEN_POWERSTATE` | `99ff10e7-23b1-4c07-a9d1-5c3206d741b4` | 0 Do nothing, 1 Turn on the display (default) |
| `SUB_SLEEP` | `GUID_SLEEP_SUBGROUP` | `238c9fa8-0aad-41ed-83f4-97be242c8f20` | subgroup |
| `STANDBYIDLE` | `GUID_STANDBY_TIMEOUT` | `29f6c1db-86da-48c5-9fdb-f2b67b1f44da` | **seconds**, 0 = never |
| `HIBERNATEIDLE` | `GUID_HIBERNATE_TIMEOUT` | `9d7815a6-7ee4-497e-8888-515a05f02364` | seconds, 0 = never |
| `HYBRIDSLEEP` | `GUID_HIBERNATE_FASTS4_POLICY` | `94ac6d29-73ce-41a6-809f-6363ba21b47e` | 0 off, 1 on |
| `UNATTENDSLEEP` | `GUID_UNATTEND_SLEEP_TIMEOUT` | `7bc4a2f9-d8fc-4469-b07b-33eb785aaca0` | seconds |
| `AWAYMODE` / `RTCWAKE` | `GUID_ALLOW_AWAYMODE` / `GUID_ALLOW_RTC_WAKE` | `25dfa149-5dd1-4736-b5ab-e8a37b5b8187` / `bd3b718a-0680-4d9d-8ab2-e1d2b4ac806d` | 0/1 |
| `SUB_VIDEO` | `GUID_VIDEO_SUBGROUP` | `7516b95f-f776-4464-8c53-06167f40cc99` | subgroup |
| `VIDEOIDLE` | `GUID_VIDEO_POWERDOWN_TIMEOUT` | `3c0bc021-c8a8-4e07-a973-6b14cbcb2b7e` | seconds, 0 = never |
| `VIDEOCONLOCK` | `GUID_VIDEO_CONSOLE_LOCK_TIMEOUT` | `8ec4b3a5-6868-48c2-be75-4f3044be88a7` | seconds (display-off timeout on lock screen) |
| `VIDEODIM` | `GUID_VIDEO_DIM_TIMEOUT` | `17aaa29b-8b43-4b94-aafe-35f64daaf1ee` | seconds |
| `SUB_DISK` / `DISKIDLE` | `GUID_DISK_SUBGROUP` / `GUID_DISK_POWERDOWN_TIMEOUT` | `0012ee47-9041-4b5d-9b77-535fba8b1442` / `6738e2c4-e8a5-4a42-b16a-e040e769756e` | seconds, 0 = never |
| `SUB_BATTERY` | `GUID_BATTERY_SUBGROUP` | `e73a048d-bf27-4f12-9731-8b2076e8891f` | subgroup |
| notif. only | `GUID_LIDSWITCH_STATE_CHANGE` | `ba3e0f4d-b817-4094-a2d1-d56379e6a0f3` | Data DWORD: 0 closed, 1 open |
| notif. only | `GUID_ACDC_POWER_SOURCE` | `5d3e9a59-e9d5-4b00-a6bd-ff34ff516548` | `SYSTEM_POWER_CONDITION`: 0 PoAc, 1 PoDc, 2 PoHot (UPS) |
| notif. only | `GUID_BATTERY_PERCENTAGE_REMAINING` | `a7ad8041-b45a-4cae-87a3-eecbb468a9e1` | 0–100 |
| notif. only | `GUID_CONSOLE_DISPLAY_STATE` / `GUID_SESSION_DISPLAY_STATUS` | `6fe69556-704a-47a0-8f24-c28d936fda47` / `2b84c20e-ad23-4ddf-93db-05ffbd7efca5` | 0 off, 1 on, 2 dimmed (use SESSION_* in user apps) |
| notif. only | `GUID_POWER_SAVING_STATUS` | `e00958c0-c213-4ace-ac77-fecced2eeea5` | 0/1 battery saver |

Key gotcha: the per-setting index for the timeout settings is in **seconds**; `powercfg /change` takes **minutes**. The button/lid "actions" are plain enum indexes (not `POWER_ACTION`).

## 2. powercfg command lines

All commands below are `%SystemRoot%\System32\powercfg.exe …` (get the dir from `windows.GetSystemDirectory()`); spawn with `SysProcAttr{HideWindow:true, CreationFlags: windows.CREATE_NO_WINDOW}` or a GUI (`-H windowsgui`) app flashes a console. Aliases are accepted wherever a GUID is.

| Purpose | Command | Admin |
|---|---|---|
| Active scheme | `powercfg /getactivescheme` → `Power Scheme GUID: 381b4222-…  (Balanced)` | no |
| Lid close (AC / DC) | `powercfg /setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 1` / `powercfg /setdcvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 1` | no* |
| Power button | `… SUB_BUTTONS PBUTTONACTION <0-4>` | no* |
| Sleep button | `… SUB_BUTTONS SBUTTONACTION <0-4>` | no* |
| Lid open wake | `… SUB_BUTTONS LIDOPENWAKE <0|1>` | no* |
| **Apply** (always after set*valueindex) | `powercfg /setactive SCHEME_CURRENT` (alias `/s`) | no* |
| Display off timeout | `powercfg /change monitor-timeout-ac <min>` / `monitor-timeout-dc` (alias `/x`; 0 = never) | no* |
| Sleep timeout | `powercfg /change standby-timeout-ac <min>` / `standby-timeout-dc` | no* |
| Hibernate timeout | `powercfg /change hibernate-timeout-ac <min>` / `hibernate-timeout-dc` | no* |
| Disk timeout | `powercfg /change disk-timeout-ac <min>` / `disk-timeout-dc` | no* |
| Same via index (seconds!) | `powercfg /setacvalueindex SCHEME_CURRENT SUB_VIDEO VIDEOIDLE 600` then `/setactive SCHEME_CURRENT` | no* |
| Hibernate on/off | `powercfg /h on` / `powercfg /h off` (`/hibernate`; also `/h /type reduced|full`, `/h /size <pct>`) | **yes** |
| Read one setting | `powercfg /q SCHEME_CURRENT SUB_BUTTONS LIDACTION` (`/query`; `/qh` includes hidden settings) | no |
| Sleep states | `powercfg /a` (`/availablesleepstates`) | no |
| Diagnostics | `powercfg /requests` (who blocks sleep; admin), `/lastwake`, `/aliases`, `/list` | varies |

`*` = allowed for standard users by default (see §4); Group Policy can lock individual settings.

`/change` applies to the active scheme on both AC/DC variants as named. `/setactive SCHEME_CURRENT` re-applies the current scheme; without it the kernel policy keeps the old values until the next scheme switch/reboot (the API doc for `PowerWriteACValueIndex` states this explicitly).

`powercfg /q SCHEME_CURRENT SUB_BUTTONS LIDACTION` output (English locale; labels are localized, numbers are not):
```
Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Balanced)
  Subgroup GUID: 4f971e89-eebd-4455-a8de-9e59040e7347  (Power buttons and lid)
    GUID Alias: SUB_BUTTONS
    Power Setting GUID: 5ca83367-6e45-459f-a27b-476b1d01c936  (Lid close action)
      GUID Alias: LIDACTION
      Possible Setting Index: 000
      Possible Setting Friendly Name: Do nothing
      Possible Setting Index: 001
      Possible Setting Friendly Name: Sleep
      Possible Setting Index: 002
      Possible Setting Friendly Name: Hibernate
      Possible Setting Index: 003
      Possible Setting Friendly Name: Shut down
      Current AC Power Setting Index: 0x00000001
      Current DC Power Setting Index: 0x00000001
```
Range settings (`VIDEOIDLE` etc.) print `Minimum Possible Setting: 0x00000000`, `Maximum Possible Setting: 0xffffffff`, `Possible Settings increment: 0x00000001`, `Possible Settings units: Seconds` before the two `Current … Index` lines. Robust, locale-independent parse: take all `0x[0-9A-Fa-f]{8}` tokens; the **last two** are AC then DC. Prefer the API for reads (no parsing, no code-page issues).

## 3. powrprof.dll API (no shell)

All exported by `powrprof.dll` (Vista+; `PowerSettingRegisterNotification` Win8+). `DWORD` return = Win32 error (0 = `ERROR_SUCCESS`). `RootPowerKey`/`UserRootPowerKey` must be `NULL`.

```c
DWORD   PowerGetActiveScheme(HKEY NULL, GUID **ActivePolicyGuid);          // free with LocalFree
DWORD   PowerSetActiveScheme(HKEY NULL, const GUID *SchemeGuid);           // required after writes
DWORD   PowerReadACValueIndex (HKEY NULL, const GUID *Scheme, const GUID *Sub, const GUID *Setting, LPDWORD AcValueIndex);
DWORD   PowerReadDCValueIndex (HKEY NULL, const GUID *Scheme, const GUID *Sub, const GUID *Setting, LPDWORD DcValueIndex);
DWORD   PowerWriteACValueIndex(HKEY NULL, const GUID *Scheme, const GUID *Sub, const GUID *Setting, DWORD AcValueIndex);
DWORD   PowerWriteDCValueIndex(HKEY NULL, const GUID *Scheme, const GUID *Sub, const GUID *Setting, DWORD DcValueIndex);
DWORD   PowerReadPossibleFriendlyName(HKEY NULL, const GUID *Sub, const GUID *Setting, ULONG PossibleSettingIndex, PUCHAR Buffer /*UTF-16*/, LPDWORD BufferSize);
DWORD   PowerReadValueMin/Max(HKEY NULL, const GUID *Sub, const GUID *Setting, LPDWORD v);   // range settings
DWORD   PowerReadFriendlyName(HKEY NULL, const GUID *Scheme, const GUID *Sub, const GUID *Setting, PUCHAR Buffer, LPDWORD BufferSize); // Sub=Setting=NULL → scheme name
BOOLEAN SetSuspendState(BOOLEAN bHibernate, BOOLEAN bForce /*no effect*/, BOOLEAN bWakeupEventsDisabled); // needs SE_SHUTDOWN_NAME enabled
BOOLEAN GetPwrCapabilities(PSYSTEM_POWER_CAPABILITIES);                  // LidPresent, SystemS3/S4, HiberFilePresent, AoAc, …
BOOLEAN IsPwrHibernateAllowed(void);                                     // legacy: S4 supported && hiberfil present
DWORD   PowerSettingRegisterNotification(LPCGUID SettingGuid, DWORD Flags /*DEVICE_NOTIFY_CALLBACK=2*/, HANDLE Recipient /*PDEVICE_NOTIFY_SUBSCRIBE_PARAMETERS*/, PHPOWERNOTIFY h);
DWORD   PowerSettingUnregisterNotification(HPOWERNOTIFY h);
DWORD   PowerRegisterSuspendResumeNotification(DWORD Flags /*=2*/, HANDLE Recipient /*same struct*/, PHPOWERNOTIFY h); // Type = PBT_APMSUSPEND 4 / PBT_APMRESUMEAUTOMATIC 0x12 / PBT_APMRESUMESUSPEND 7
typedef ULONG CALLBACK DEVICE_NOTIFY_CALLBACK_ROUTINE(PVOID Context, ULONG Type, PVOID Setting); // Setting → POWERBROADCAST_SETTING*
typedef struct { PDEVICE_NOTIFY_CALLBACK_ROUTINE Callback; PVOID Context; } DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS;
typedef struct { GUID PowerSetting; DWORD DataLength; UCHAR Data[1]; } POWERBROADCAST_SETTING;   // Data = DWORD for the GUIDs above
```
Enabling/disabling hibernation has no documented API; `CallNtPowerInformation(SystemReserveHiberFile /*=10*/, &BOOLEAN, 1, NULL, 0)` is what `powercfg /h` uses but is undocumented — stick to `powercfg /h` in the elevated helper.

### Other Win32 pieces
| Function (DLL) | Signature / constants |
|---|---|
| `SetThreadExecutionState` (kernel32) | `EXECUTION_STATE f(EXECUTION_STATE)`; `ES_SYSTEM_REQUIRED 0x1`, `ES_DISPLAY_REQUIRED 0x2`, `ES_AWAYMODE_REQUIRED 0x40` (S3-only, only with CONTINUOUS), `ES_CONTINUOUS 0x80000000`; returns previous state, 0 on failure; **per calling thread** |
| `PowerCreateRequest` / `PowerSetRequest` / `PowerClearRequest` (kernel32) | `HANDLE PowerCreateRequest(PREASON_CONTEXT)` (INVALID_HANDLE_VALUE on fail; `CloseHandle` frees); `BOOL PowerSetRequest(HANDLE, POWER_REQUEST_TYPE)`; types: `PowerRequestDisplayRequired 0`, `PowerRequestSystemRequired 1`, `PowerRequestAwayModeRequired 2`, `PowerRequestExecutionRequired 3`. `REASON_CONTEXT{ULONG Version=POWER_REQUEST_CONTEXT_VERSION(0); DWORD Flags=POWER_REQUEST_CONTEXT_SIMPLE_STRING(1); LPWSTR SimpleReasonString}` |
| `GetSystemPowerStatus` (kernel32) | `BOOL f(LPSYSTEM_POWER_STATUS)`; struct `{BYTE ACLineStatus; BYTE BatteryFlag; BYTE BatteryLifePercent; BYTE SystemStatusFlag; DWORD BatteryLifeTime; DWORD BatteryFullLifeTime}` = 12 bytes. ACLineStatus 0 offline, 1 online, 255 unknown. BatteryFlag bits 1 high(>66%), 2 low(<33%), 4 critical(<5%), 8 charging, 128 no battery, 255 unknown. SystemStatusFlag 1 = battery saver on. Percent 255 / times 0xFFFFFFFF = unknown |
| `LockWorkStation` (user32) | `BOOL f(void)`; async; only from the interactive desktop; already-locked → succeeds without effect |
| `SendMessageTimeoutW` (user32) | `HWND_BROADCAST 0xFFFF`, `WM_SYSCOMMAND 0x0112`, `SC_MONITORPOWER 0xF170`, lParam `2` off, `1` low power, `-1` on; `SMTO_ABORTIFHUNG 0x2` |
| `ExitWindowsEx` (user32; in x/sys) | `BOOL f(UINT uFlags, DWORD dwReason)`; `EWX_LOGOFF 0`, `EWX_SHUTDOWN 0x1` (does **not** power off), `EWX_REBOOT 0x2`, `EWX_FORCE 0x4`, `EWX_POWEROFF 0x8`, `EWX_FORCEIFHUNG 0x10`, `EWX_RESTARTAPPS 0x40`, `EWX_HYBRID_SHUTDOWN 0x400000` (combine with `EWX_SHUTDOWN`); reason `SHTDN_REASON_MAJOR_OTHER 0 | SHTDN_REASON_MINOR_OTHER 0 | SHTDN_REASON_FLAG_PLANNED 0x80000000` (never pass 0); needs `SeShutdownPrivilege`; returns as soon as shutdown is initiated |
| `InitiateSystemShutdownExW` / `AbortSystemShutdownW` (advapi32; first is in x/sys as `windows.InitiateSystemShutdownEx`) | `BOOL f(LPWSTR machine=NULL, LPWSTR msg, DWORD timeoutSec, BOOL force, BOOL reboot, DWORD reason)`; timeout>0 shows countdown and is abortable; `ERROR_SHUTDOWN_IN_PROGRESS 1115`, `ERROR_SHUTDOWN_IS_SCHEDULED 1190`, `ERROR_NO_SHUTDOWN_IN_PROGRESS 1116` (abort with nothing pending) |
| `RegisterPowerSettingNotification` (user32) | `HPOWERNOTIFY f(HANDLE hwnd, LPCGUID, DWORD Flags=DEVICE_NOTIFY_WINDOW_HANDLE 0)`; delivered as `WM_POWERBROADCAST 0x0218`, wParam `PBT_POWERSETTINGCHANGE 0x8013`, lParam → `POWERBROADCAST_SETTING*`; return TRUE. Use the WebView2 host window's WndProc, or skip windows entirely with the powrprof callback variant |
| `ShellExecuteExW` (shell32) | `BOOL f(SHELLEXECUTEINFOW*)`; `cbSize` 112 (x64)/60 (x86); `fMask`: `SEE_MASK_NOCLOSEPROCESS 0x40` (get `hProcess`), `SEE_MASK_NOASYNC 0x100` (required when the caller has no message loop), `SEE_MASK_FLAG_NO_UI 0x400`, `SEE_MASK_NO_CONSOLE 0x8000`; verb `runas`; on failure `GetLastError()==ERROR_CANCELLED (1223)` when the user declines UAC; docs recommend `CoInitializeEx(NULL, COINIT_APARTMENTTHREADED|COINIT_DISABLE_OLE1DDE)` first. x/sys `windows.ShellExecute` (non-Ex) only gives you an error, no process handle |
| `CheckTokenMembership` (advapi32; x/sys `Token.IsMember`) | `BOOL f(HANDLE Token /*NULL = caller*/, PSID, PBOOL)`; Administrators SID = `S-1-5-32-544` (`SECURITY_BUILTIN_DOMAIN_RID 0x20`, `DOMAIN_ALIAS_RID_ADMINS 0x220`) or `CreateWellKnownSid(WinBuiltinAdministratorsSid=26)`; under UAC the unelevated admin token carries the group as deny-only → returns FALSE. Pass token **0**, not the pseudo process token (must be an impersonation token or NULL) |
| `GetTokenInformation(TokenElevation)` (x/sys `Token.IsElevated()`) | TRUE only in an elevated process (or UAC off) |
| `IsUserAnAdmin` (shell32) | `BOOL f(void)`; deprecated (not in current SDK `shellapi.h`), still exported; equivalent to the CheckTokenMembership check above |

x/sys v0.33.0 (already in go.mod) provides: `ShellExecute`, `ExitWindowsEx`, `InitiateSystemShutdownEx`, `OpenProcessToken`, `LookupPrivilegeValue`, `AdjustTokenPrivileges`, `Tokenprivileges`/`LUIDAndAttributes`, `SE_PRIVILEGE_ENABLED`, `CreateWellKnownSid`, `Token.IsMember`, `Token.IsElevated`, `GetCurrentProcessToken`, `LocalFree`, `GUID`, `GUIDFromString` (braces required), `NewCallback`, `CoInitializeEx`, `COINIT_*`, `WaitForSingleObject`, `GetExitCodeProcess`, `GetSystemDirectory`, `CREATE_NO_WINDOW`, `ERROR_CANCELLED`, `ERROR_NOT_ALL_ASSIGNED`, `ERROR_SHUTDOWN_IS_SCHEDULED`. **Not** provided (use `NewLazySystemDLL(...).NewProc`): every `powrprof.dll` function, `SetThreadExecutionState`, `GetSystemPowerStatus`, `Power*Request`, `LockWorkStation`, `SendMessageTimeoutW`, `RegisterPowerSettingNotification`, `ShellExecuteExW`, `AbortSystemShutdownW`.

## 4. Permissions

| Operation | Needs | How to detect / obtain |
|---|---|---|
| Read any setting (`/q`, `PowerRead*`) | nothing | — |
| `set{ac,dc}valueindex`, `/change`, `/setactive`, `PowerWrite*`, `PowerSetActiveScheme` | standard user by default ("change power management settings" is an explicit Vista+ standard-user task; the Control Panel pages do this without a UAC shield) | Group Policy (`HKLM\SOFTWARE\Policies\Microsoft\Power\PowerSettings\<setting GUID>`) can lock a setting → write returns `ERROR_ACCESS_DENIED` (5) or is overridden; surface the error, do not auto-elevate |
| `powercfg /h on|off` (create/delete `hiberfil.sys`, toggles Fast Startup) | elevation (`SeCreatePagefilePrivilege`; non-elevated runs fail with access denied / error 0x65) | relaunch helper elevated (§5 sketch) |
| `schtasks /create` with `/ru SYSTEM` or `/rl HIGHEST`; writing `HKLM\…\Session Manager\Power\HiberbootEnabled` | elevation | same helper |
| `SetSuspendState`, `ExitWindowsEx`, `InitiateSystemShutdownExW`, `AbortSystemShutdownW`, `shutdown.exe` | `SeShutdownPrivilege` **enabled** in the process token (interactive users hold it disabled by default: "By default, users can enable the SE_SHUTDOWN_NAME privilege on the computer they are logged onto") | `AdjustTokenPrivileges` once at startup; check for `ERROR_NOT_ALL_ASSIGNED` (1300) |
| `LockWorkStation`, `SC_MONITORPOWER`, `SetThreadExecutionState`, `Power*Request`, notifications | nothing (interactive desktop) | — |

Elevation mechanics for a Go GUI app:
- Detect: `windows.GetCurrentProcessToken().IsElevated()`; whether UAC will ask for *consent* (admin user, split token) or *credentials* (standard user): `GetTokenInformation(TokenElevationType)` == `TokenElevationTypeLimited` (3) ⇒ consent prompt.
- Relaunch: `ShellExecuteExW{lpVerb:"runas", lpFile: os.Executable(), lpParameters: "--elevated <op> …", fMask: NOCLOSEPROCESS|NOASYNC}` → wait on `hProcess`, read exit code. Keep the elevated child **headless and single-purpose** (run one `powercfg` command, exit); do not elevate the whole WebView2 UI.
- UAC implications: (1) the user may cancel → `ERROR_CANCELLED`; (2) for a standard user the elevated child runs as the *admin account whose credentials were typed* (different HKCU/`%APPDATA%`; don't read the app's per-user config there, pass everything on the command line); (3) elevated and non-elevated processes can't drag/drop or send window messages to each other (UIPI) — communicate via exit code/stdout file; (4) with UAC disabled the admin's process is already elevated and `runas` just launches; (5) an unmanifested exe named `*setup*`/`*install*`/`*update*` triggers installer detection → add an `asInvoker` manifest.

## 5. Immediate actions, keep-awake, scheduling

- **Sleep**: enable `SeShutdownPrivilege`, `SetSuspendState(0,0,0)`. **Modern Standby (S0ix) systems**: reported to fail with `ERROR_NOT_SUPPORTED` (50) (OSR thread; Surface "sleep shortcut not working"); detect via `GetPwrCapabilities().AoAc == TRUE` / `SystemS3 == FALSE` and fall back to turning the display off (that *is* the Modern Standby sleep entry) and tell the user. `rundll32 powrprof.dll,SetSuspendState 0,1,0` is not an alternative: rundll32 passes string pointers, so `bHibernate` is non-zero and the box hibernates when hibernation is enabled.
- **Hibernate**: `SetSuspendState(1,0,0)` (fails when `GetPwrCapabilities().HiberFilePresent == FALSE`), or `shutdown.exe /h`.
- **Lock**: `LockWorkStation()`.
- **Display off**: `SendMessageTimeoutW(HWND_BROADCAST, WM_SYSCOMMAND, SC_MONITORPOWER, 2, SMTO_ABORTIFHUNG, 1000, NULL)` after a ~300–500 ms delay so the click/keyup that triggered it doesn't immediately wake the panel. lParam `-1` (turn on) is unreliable on Win8+; wake with a synthetic mouse move (`SendInput`) instead.
- **Shut down / restart**: `ExitWindowsEx(EWX_POWEROFF|EWX_FORCEIFHUNG, SHTDN_REASON_MAJOR_OTHER|SHTDN_REASON_FLAG_PLANNED)`, `EWX_REBOOT|…` for restart, `EWX_SHUTDOWN|EWX_HYBRID_SHUTDOWN` for a fast-startup shutdown. `EWX_SHUTDOWN` alone leaves power on. Returns TRUE as soon as shutdown is *initiated*; apps with unsaved data can still block it unless `EWX_FORCE`.
- **Timed one-off**: `shutdown.exe /s /t N [/c "msg"]` (N seconds, 0–315360000; N>0 implies `/f`; `/r` restart, `/h` hibernate, `/hybrid`, `/p` immediate power-off); cancel with `shutdown.exe /a`. API equivalent: `InitiateSystemShutdownExW(NULL, msg, N, TRUE, restart, reason)` + `AbortSystemShutdownW(NULL)`. Only one pending shutdown at a time (`ERROR_SHUTDOWN_IS_SCHEDULED` 1190 / `shutdown.exe` prints "A system shutdown has already been scheduled.(1190)").
- **Recurring**: `schtasks /create /tn "Vitals\Shutdown" /sc daily /st 23:00 /tr "\"%SystemRoot%\System32\shutdown.exe\" /s /t 60 /c \"Vitals scheduled shutdown\"" /f`; weekly: `/sc weekly /d MON,TUE,WED,THU,FRI /st 23:00`; remove: `schtasks /delete /tn "Vitals\Shutdown" /f`; inspect: `schtasks /query /tn "Vitals\Shutdown" /xml`. `/st` is 24h `HH:mm`; avoid `/sd` (locale-formatted); `/tr` ≤ 262 chars; `/ru SYSTEM`, `/rl HIGHEST` need elevation. A task created by a standard user without `/ru` runs as that user and only while they are logged on (locked is fine).
- **Keep-awake**: `SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED[|ES_DISPLAY_REQUIRED])`, clear with `SetThreadExecutionState(ES_CONTINUOUS)`; state is bound to the OS thread → in Go run it in a goroutine that called `runtime.LockOSThread()` and stays alive (if that goroutine exits, Go kills the thread and the state silently disappears). Preferred: `PowerCreateRequest` + `PowerSetRequest(h, PowerRequestSystemRequired)` (+ `PowerRequestDisplayRequired` — the docs say take *both* to keep the display on and the system awake), `PowerClearRequest`, `CloseHandle`; shows up in `powercfg /requests` under the process name with your reason string. Neither prevents lid-close/power-button/Start-menu sleep ("power requests are terminated upon user-initiated system sleep entry"); on Modern Standby on battery, system/execution requests are terminated 5 min after the sleep timeout.
- **Lid / AC state**: `GetSystemPowerStatus` for AC/battery/battery-saver (poll or react to `PBT_APMPOWERSTATUSCHANGE 0xA`); `GetPwrCapabilities().LidPresent` to decide whether to show lid settings (desktops still have `LIDACTION` in the scheme, just no lid); lid open/closed via `PowerSettingRegisterNotification(GUID_LIDSWITCH_STATE_CHANGE, DEVICE_NOTIFY_CALLBACK, &params, &h)` — the callback fires immediately with the current value, but "won't be called until a lid device is found and its current state is known" (never on lid-less machines). Same call with `GUID_ACDC_POWER_SOURCE`, `GUID_BATTERY_PERCENTAGE_REMAINING`, `GUID_SESSION_DISPLAY_STATUS`.

## 6. Pitfalls (beyond the ones above)
1. Forgetting `/setactive SCHEME_CURRENT` / `PowerSetActiveScheme` after writes — the registry changes but behaviour doesn't until reboot/scheme switch.
2. `/change` is minutes, value indexes are seconds; `0` = never in both. Max index `0xffffffff`.
3. `powercfg /h off` deletes `hiberfil.sys`, disables Fast Startup/hybrid sleep and makes "Hibernate" (index 2) invalid for lid/buttons/`UIBUTTON_ACTION` and the hibernate timeouts — reset those to Sleep (1) first or the UI shows stale choices. `/h /type reduced` keeps Fast Startup but removes the Hibernate option.
4. `powercfg /q` text is localized (German/French labels, OEM code page when redirected). Parse only hex/GUID tokens or use the API.
5. Modern Standby: `powercfg /a` lists `Standby (S0 Low Power Idle) Network Connected` and `S3` as unavailable; `SetSuspendState` may fail; `ES_AWAYMODE_REQUIRED`/`PowerRequestAwayModeRequired` are S3-only.
6. `AdjustTokenPrivileges` returns TRUE even if nothing was granted — check `ERROR_NOT_ALL_ASSIGNED` from the *same* syscall (`proc.Call` returns it as `e1`); don't call `GetLastError` later from Go (the goroutine may have moved threads).
7. `ExitWindowsEx` from a non-interactive context (service/session 0) succeeds without shutting down; use `InitiateSystemShutdownExW` there.
8. `windows.NewCallback` callbacks run on system thread-pool threads: never block, never call back into power APIs from inside; push to a channel. Keep the `DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS` struct referenced from Go for the registration's lifetime (GC). `NewCallback` is limited to ~2000 callbacks per process — create once.
9. `CheckTokenMembership` with the pseudo process token (`GetCurrentProcessToken()`, value `-4`) fails (`ERROR_NO_IMPERSONATION_TOKEN`); pass `0`.
10. `Token.IsMember(admins)` is FALSE for an admin running unelevated (deny-only SID) — use it to answer "is this user an admin at all?" only via the linked token / `TokenElevationType`.
11. 32-bit Go on 64-bit Windows: `System32` is redirected to `SysWOW64` (a 32-bit `powercfg.exe` exists there, but prefer building `amd64`/`arm64`).
12. `SHELLEXECUTEINFOW.cbSize` must be `unsafe.Sizeof` of a struct with the right padding (112 bytes on x64) or `ShellExecuteExW` fails with `ERROR_INVALID_PARAMETER`.
13. `REASON_CONTEXT` is a union: allocate the full size (32 bytes x64) even when using `SimpleReasonString`.

## 7. Go sketches (`//go:build windows`, `golang.org/x/sys/windows`)

```go
var (
	powrprof = windows.NewLazySystemDLL("powrprof.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	pGetActiveScheme = powrprof.NewProc("PowerGetActiveScheme")
	pSetActiveScheme = powrprof.NewProc("PowerSetActiveScheme")
	pReadAC, pReadDC = powrprof.NewProc("PowerReadACValueIndex"), powrprof.NewProc("PowerReadDCValueIndex")
	pWriteAC, pWriteDC = powrprof.NewProc("PowerWriteACValueIndex"), powrprof.NewProc("PowerWriteDCValueIndex")
	pSetSuspendState = powrprof.NewProc("SetSuspendState")
	pGetPwrCaps      = powrprof.NewProc("GetPwrCapabilities")
	pRegNotify       = powrprof.NewProc("PowerSettingRegisterNotification")
	pUnregNotify     = powrprof.NewProc("PowerSettingUnregisterNotification")
	pSetThreadExec   = kernel32.NewProc("SetThreadExecutionState")
	pGetPowerStatus  = kernel32.NewProc("GetSystemPowerStatus")
	pPowerCreateReq  = kernel32.NewProc("PowerCreateRequest")
	pPowerSetReq     = kernel32.NewProc("PowerSetRequest")
	pPowerClearReq   = kernel32.NewProc("PowerClearRequest")
	pLockWorkStation = user32.NewProc("LockWorkStation")
	pSendMsgTimeout  = user32.NewProc("SendMessageTimeoutW")
	pAdjustPrivs     = advapi32.NewProc("AdjustTokenPrivileges")
	pAbortShutdown   = advapi32.NewProc("AbortSystemShutdownW")
	pShellExecuteEx  = shell32.NewProc("ShellExecuteExW")
)

func mustGUID(s string) windows.GUID { g, err := windows.GUIDFromString(s); if err != nil { panic(err) }; return g }
var (
	SubButtons   = mustGUID("{4F971E89-EEBD-4455-A8DE-9E59040E7347}")
	LidAction    = mustGUID("{5CA83367-6E45-459F-A27B-476B1D01C936}")
	PButtonAct   = mustGUID("{7648EFA3-DD9C-4E3E-B566-50F929386280}")
	SButtonAct   = mustGUID("{96996BC0-AD50-47EC-923B-6F41874DD9EB}")
	LidOpenWake  = mustGUID("{99FF10E7-23B1-4C07-A9D1-5C3206D741B4}")
	SubSleep     = mustGUID("{238C9FA8-0AAD-41ED-83F4-97BE242C8F20}")
	StandbyIdle  = mustGUID("{29F6C1DB-86DA-48C5-9FDB-F2B67B1F44DA}")
	HibernateIdle= mustGUID("{9D7815A6-7EE4-497E-8888-515A05F02364}")
	SubVideo     = mustGUID("{7516B95F-F776-4464-8C53-06167F40CC99}")
	VideoIdle    = mustGUID("{3C0BC021-C8A8-4E07-A973-6B14CBCB2B7E}")
	SubDisk      = mustGUID("{0012EE47-9041-4B5D-9B77-535FBA8B1442}")
	DiskIdle     = mustGUID("{6738E2C4-E8A5-4A42-B16A-E040E769756E}")
	GuidLidSwitch= mustGUID("{BA3E0F4D-B817-4094-A2D1-D56379E6A0F3}")
	GuidACDC     = mustGUID("{5D3E9A59-E9D5-4B00-A6BD-FF34FF516548}")
)

// --- read / write a value index via the API (no shell) ---------------------------------
func withActiveScheme(f func(scheme *windows.GUID) error) error {
	var scheme *windows.GUID
	if r, _, _ := pGetActiveScheme.Call(0, uintptr(unsafe.Pointer(&scheme))); r != 0 { return syscall.Errno(r) }
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(scheme)))
	return f(scheme)
}
func ReadIndex(sub, setting *windows.GUID) (ac, dc uint32, err error) {
	err = withActiveScheme(func(s *windows.GUID) error {
		for _, p := range []struct{ proc *windows.LazyProc; out *uint32 }{{pReadAC, &ac}, {pReadDC, &dc}} {
			if r, _, _ := p.proc.Call(0, uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(sub)),
				uintptr(unsafe.Pointer(setting)), uintptr(unsafe.Pointer(p.out))); r != 0 { return syscall.Errno(r) }
		}
		return nil
	})
	return
}
// WriteIndex writes AC and/or DC (nil = leave) and re-activates the scheme (== powercfg /setactive SCHEME_CURRENT).
func WriteIndex(sub, setting *windows.GUID, ac, dc *uint32) error {
	return withActiveScheme(func(s *windows.GUID) error {
		for _, p := range []struct{ proc *windows.LazyProc; v *uint32 }{{pWriteAC, ac}, {pWriteDC, dc}} {
			if p.v == nil { continue }
			if r, _, _ := p.proc.Call(0, uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(sub)),
				uintptr(unsafe.Pointer(setting)), uintptr(*p.v)); r != 0 { return syscall.Errno(r) } // 5 = locked by policy
		}
		if r, _, _ := pSetActiveScheme.Call(0, uintptr(unsafe.Pointer(s))); r != 0 { return syscall.Errno(r) }
		return nil
	})
}
// Timeouts via the API are SECONDS: WriteIndex(&SubVideo, &VideoIdle, u32(10*60), u32(5*60))

// --- powercfg child (hidden console) and /q parsing ------------------------------------
func powercfg(args ...string) (string, error) {
	sys, _ := windows.GetSystemDirectory()
	cmd := exec.Command(filepath.Join(sys, "powercfg.exe"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput() // powercfg prints its errors to stdout; exit code != 0 on failure
	return string(out), err
}
var hexIdx = regexp.MustCompile(`0x[0-9A-Fa-f]{8}`)
func QueryIndex(sub, setting string) (ac, dc uint32, err error) { // sub/setting: aliases or GUIDs
	out, err := powercfg("/q", "SCHEME_CURRENT", sub, setting)
	if err != nil { return 0, 0, fmt.Errorf("powercfg: %w: %s", err, out) }
	m := hexIdx.FindAllString(out, -1) // labels are localized; the last two hex tokens are AC then DC
	if len(m) < 2 { return 0, 0, fmt.Errorf("unexpected powercfg output: %q", out) }
	a, _ := strconv.ParseUint(m[len(m)-2][2:], 16, 32); d, _ := strconv.ParseUint(m[len(m)-1][2:], 16, 32)
	return uint32(a), uint32(d), nil
}
// Lid close on battery → sleep, then apply:
//   powercfg("/setdcvalueindex", "SCHEME_CURRENT", "SUB_BUTTONS", "LIDACTION", "1"); powercfg("/setactive", "SCHEME_CURRENT")
// Display off after 10 min on AC:  powercfg("/change", "monitor-timeout-ac", "10")

// --- admin detection and elevated relaunch ---------------------------------------------
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
func IsAdminMember() bool { // FALSE for an admin running unelevated under UAC
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); if err != nil { return false }
	ok, err := windows.Token(0).IsMember(sid) // 0 = "calling thread / duplicated primary token"; NOT GetCurrentProcessToken()
	return err == nil && ok
}
type shellExecuteInfoW struct { // SHELLEXECUTEINFOW; unsafe.Sizeof == 112 on amd64/arm64
	cbSize, fMask uint32; hwnd windows.Handle
	lpVerb, lpFile, lpParameters, lpDirectory *uint16; nShow int32
	hInstApp windows.Handle; lpIDList uintptr; lpClass *uint16; hkeyClass windows.Handle
	dwHotKey uint32; hIconOrMonitor windows.Handle; hProcess windows.Handle
}
var ErrUserDeclined = errors.New("elevation declined")
// RunElevated runs this exe again as "<exe> --elevated <args…>" and returns the child's exit code.
func RunElevated(args string) (uint32, error) {
	runtime.LockOSThread(); defer runtime.UnlockOSThread()
	windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE) // per ShellExecuteEx docs
	exe, _ := os.Executable()
	info := shellExecuteInfoW{fMask: 0x40 | 0x100 /*NOCLOSEPROCESS|NOASYNC*/, nShow: windows.SW_HIDE,
		lpVerb: windows.StringToUTF16Ptr("runas"), lpFile: windows.StringToUTF16Ptr(exe),
		lpParameters: windows.StringToUTF16Ptr("--elevated " + args)}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, e := pShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(e, windows.ERROR_CANCELLED) { return 0, ErrUserDeclined } // user hit "No" on the UAC prompt
		return 0, e
	}
	defer windows.CloseHandle(info.hProcess)
	windows.WaitForSingleObject(info.hProcess, windows.INFINITE)
	var code uint32; windows.GetExitCodeProcess(info.hProcess, &code)
	return code, nil
}
// In main(): if len(os.Args) > 2 && os.Args[1] == "--elevated" { _, err := powercfg(os.Args[2:]...); exit(0 or 1) }
// Caller: RunElevated(`/h off`)   // only /h needs this; everything else runs unelevated

// --- privileges and immediate actions ---------------------------------------------------
func EnablePrivilege(name string) error { // "SeShutdownPrivilege"
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil { return err }
	defer tok.Close()
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &tp.Privileges[0].Luid); err != nil { return err }
	tp.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	r, _, e := pAdjustPrivs.Call(uintptr(tok), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0) // raw call: need e1 even when r != 0
	if r == 0 { return e }
	if errors.Is(e, windows.ERROR_NOT_ALL_ASSIGNED) { return e } // privilege not held (e.g. denied by policy)
	return nil
}
func Suspend(hibernate bool) error {
	if err := EnablePrivilege("SeShutdownPrivilege"); err != nil { return err }
	var h uintptr; if hibernate { h = 1 }
	if r, _, e := pSetSuspendState.Call(h, 0, 0); r == 0 { return e } // ERROR_NOT_SUPPORTED on Modern Standby
	return nil
}
func Lock() error { r, _, e := pLockWorkStation.Call(); if r == 0 { return e }; return nil }
func DisplayOff() { // call ~300 ms after the UI click so the input doesn't wake it again
	pSendMsgTimeout.Call(0xFFFF /*HWND_BROADCAST*/, 0x0112 /*WM_SYSCOMMAND*/, 0xF170 /*SC_MONITORPOWER*/, 2, 0x2 /*SMTO_ABORTIFHUNG*/, 1000, 0)
}
const (EWX_REBOOT, EWX_POWEROFF, EWX_FORCEIFHUNG = 0x2, 0x8, 0x10; SHTDN_PLANNED = 0x80000000)
func Shutdown(restart bool) error {
	if err := EnablePrivilege("SeShutdownPrivilege"); err != nil { return err }
	flags := uint32(EWX_POWEROFF); if restart { flags = EWX_REBOOT }
	return windows.ExitWindowsEx(flags|EWX_FORCEIFHUNG, SHTDN_PLANNED) // MAJOR_OTHER|MINOR_OTHER|FLAG_PLANNED
}
func ShutdownIn(sec uint32, restart bool) error { // countdown toast; AbortShutdown() cancels; 1190 if one is pending
	if err := EnablePrivilege("SeShutdownPrivilege"); err != nil { return err }
	return windows.InitiateSystemShutdownEx(nil, windows.StringToUTF16Ptr("Vitals: scheduled shutdown"), sec, true, restart, SHTDN_PLANNED)
}
func AbortShutdown() error { r, _, e := pAbortShutdown.Call(0); if r == 0 { return e }; return nil } // 1116 = nothing pending
// shell alternatives: powercfg-style exec of "shutdown.exe /s /t 600 /c \"...\"" and "shutdown.exe /a"

// --- keep-awake ----------------------------------------------------------------------------
type reasonContext struct { Version, Flags uint32; SimpleReasonString *uint16; _ [16]byte } // pad to full REASON_CONTEXT union size
func NewAwakeRequest(reason string) (windows.Handle, error) {
	ctx := reasonContext{Version: 0 /*POWER_REQUEST_CONTEXT_VERSION*/, Flags: 1 /*SIMPLE_STRING*/, SimpleReasonString: windows.StringToUTF16Ptr(reason)}
	h, _, e := pPowerCreateReq.Call(uintptr(unsafe.Pointer(&ctx)))
	if windows.Handle(h) == windows.InvalidHandle { return 0, e }
	return windows.Handle(h), nil
}
func SetAwake(h windows.Handle, display bool) error { // visible in `powercfg /requests`
	if r, _, e := pPowerSetReq.Call(uintptr(h), 1 /*PowerRequestSystemRequired*/); r == 0 { return e }
	if display { if r, _, e := pPowerSetReq.Call(uintptr(h), 0 /*PowerRequestDisplayRequired*/); r == 0 { return e } }
	return nil
}
// ClearAwake: pPowerClearReq.Call(h, 1) / (h, 0), then windows.CloseHandle(h). All requests die with the process.
func KeepAwakeThreadState(stop <-chan struct{}) { // SetThreadExecutionState variant: state belongs to the OS thread
	go func() {
		runtime.LockOSThread() // never unlock: the thread (and its ES_ state) is destroyed when this goroutine returns
		pSetThreadExec.Call(0x80000000 | 0x1 | 0x2) // ES_CONTINUOUS|ES_SYSTEM_REQUIRED|ES_DISPLAY_REQUIRED
		<-stop
		pSetThreadExec.Call(0x80000000) // ES_CONTINUOUS alone clears the requirements
	}()
}

// --- AC / lid state ------------------------------------------------------------------------
type systemPowerStatus struct { ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8; BatteryLifeTime, BatteryFullLifeTime uint32 }
func PowerStatus() (systemPowerStatus, error) {
	var s systemPowerStatus
	if r, _, e := pGetPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 { return s, e }
	return s, nil // s.ACLineStatus==1 → on AC; s.SystemStatusFlag&1 → battery saver; 255/0xFFFFFFFF = unknown
}
type systemPowerCapabilities struct { // SYSTEM_POWER_CAPABILITIES, 76 bytes (Win10 layout)
	PowerButtonPresent, SleepButtonPresent, LidPresent, SystemS1, SystemS2, SystemS3, SystemS4, SystemS5 bool
	HiberFilePresent, FullWake, VideoDimPresent, ApmPresent, UpsPresent, ThermalControl, ProcessorThrottle bool
	ProcessorMinThrottle, ProcessorMaxThrottle uint8
	FastSystemS4, Hiberboot, WakeAlarmPresent, AoAc, DiskSpinDown bool
	HiberFileType uint8; AoAcConnectivitySupported bool; _ [6]byte
	SystemBatteriesPresent, BatteriesAreShortTerm bool
	BatteryScale [3]struct{ Granularity, Capacity uint32 }
	AcOnLineWake, SoftLidWake, RtcWake, MinDeviceWakeState, DefaultLowLatencyWake int32
}
func Capabilities() (systemPowerCapabilities, error) {
	var c systemPowerCapabilities
	if r, _, e := pGetPwrCaps.Call(uintptr(unsafe.Pointer(&c))); r == 0 { return c, e }
	return c, nil // c.LidPresent → show lid UI; c.AoAc → Modern Standby; c.HiberFilePresent && c.SystemS4 → hibernate usable
}
type powerbroadcastSetting struct { PowerSetting windows.GUID; DataLength uint32; Data [4]byte }
type subscribeParams struct { Callback, Context uintptr }
var lidParams *subscribeParams // keep referenced: Windows holds the pointer for the registration's lifetime
var lidCallback = windows.NewCallback(func(ctx, typ, setting uintptr) uintptr { // runs on a system thread-pool thread
	s := (*powerbroadcastSetting)(unsafe.Pointer(setting)) // typ == PBT_POWERSETTINGCHANGE (0x8013)
	open := *(*uint32)(unsafe.Pointer(&s.Data[0])) == 1
	select { case lidEvents <- open: default: } // never block here
	return 0
})
var lidEvents = make(chan bool, 8)
func WatchLid() (windows.Handle, error) { // first callback arrives immediately with the current state (if a lid exists)
	lidParams = &subscribeParams{Callback: lidCallback}
	var h windows.Handle
	if r, _, _ := pRegNotify.Call(uintptr(unsafe.Pointer(&GuidLidSwitch)), 2 /*DEVICE_NOTIFY_CALLBACK*/,
		uintptr(unsafe.Pointer(lidParams)), uintptr(unsafe.Pointer(&h))); r != 0 { return 0, syscall.Errno(r) }
	return h, nil // pUnregNotify.Call(uintptr(h)) to stop; same pattern for GuidACDC (Data: 0 AC, 1 DC, 2 UPS)
}
```

## 8. Unverified (could not be confirmed from an authoritative source in this sandbox)
- The literal `powercfg /?` / Learn page wording for `/change`, `/setacvalueindex`, `/setactive`, `/h` (page blocked; semantics confirmed via search snippets of the official page and the API docs). Exact `/q` line labels above are from English Windows 10/11 output as commonly posted; verify once on a real box.
- That `/setacvalueindex`, `/setdcvalueindex`, `/change` and `/setactive` succeed **unelevated** by default: supported by Microsoft's Vista+ UAC statement that standard users can "change power management settings" and by the shield-less Control Panel pages, but not tested here. Verify on a standard-user VM (`powercfg /setdcvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 1` → exit 0, no output). The Group Policy lock path (`HKLM\SOFTWARE\Policies\Microsoft\Power\PowerSettings\<GUID>`) and the resulting error code are from third-party notes.
- `SetSuspendState` failing with `ERROR_NOT_SUPPORTED` on Modern Standby comes from an OSR community thread and user reports (Surface); it may depend on OS build/session. Test both `AoAc` and S3 machines.
- `powercfg /h off` requiring elevation is confirmed only by multiple user reports (error 0x65 / access denied) plus the privilege it needs; the official page wording was not readable.
- `shutdown.exe` exit codes mirroring Win32 errors (1190, 1116) and the exact "already been scheduled" message text.
- `IsUserAnAdmin` still being exported by name from `shell32.dll` on current builds (its doc page is deprecated; it is absent from the current SDK `shellapi.h`).
- `SC_MONITORPOWER` with `lParam=-1` being unreliable on Win8+ and the need for a short delay are field reports, not documented.
- `schtasks /create` by a standard user without `/ru` (runs as self, logged-on only) — documented behaviour for `/it`/`/ru` but the default-privilege case was not fetched.

## 9. Sources
- SDK headers (`microsoft/win32metadata`, `generation/WinSDK/RecompiledIdlHeaders`): `um/winnt.h` (GUIDs, `ES_*`, `POWER_INFORMATION_LEVEL`, `SYSTEM_POWER_CAPABILITIES`), `um/powrprof.h` (`SetSuspendState`, `DEVICE_NOTIFY_CALLBACK 2`, `DEVICE_NOTIFY_CALLBACK_ROUTINE`, `DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS`, `PowerRead*`), `um/powersetting.h` (`PowerRead/Write*ValueIndex`, `PowerGet/SetActiveScheme`, `PowerSettingRegisterNotification`), `um/powerbase.h` (`GetPwrCapabilities`, `PowerRegisterSuspendResumeNotification`), `um/WinUser.h` (`SC_MONITORPOWER`, `HWND_BROADCAST`, `WM_POWERBROADCAST`, `PBT_POWERSETTINGCHANGE`, `DEVICE_NOTIFY_*`, `SMTO_ABORTIFHUNG`), `um/WinBase.h` (`SetThreadExecutionState`, `Power*Request`, `GetSystemPowerStatus`) — https://github.com/microsoft/win32metadata
- mingw-w64 headers (`mingw-w64-headers/include/`): `winnt.h`, `winuser.h` (`EWX_*`, `PBT_*`), `winbase.h` (`SYSTEM_POWER_STATUS`, `AC_LINE_*`, `BATTERY_FLAG_*`), `shellapi.h` (`SHELLEXECUTEINFOW`, `SEE_MASK_*`), `winerror.h` (1223/1300/1190/1116/1115), `reason.h` (`SHTDN_REASON_*`) — https://github.com/mingw-w64/mingw-w64
- `MicrosoftDocs/sdk-api` (`sdk-api-src/content/…`): `powrprof/nf-powrprof-setsuspendstate.md`, `powersetting/nf-powersetting-powerwriteacvalueindex.md` (Remarks: PowerSetActiveScheme required), `powrprof/nf-powrprof-powerreadacvalueindex.md`, `powersetting/nf-powersetting-powergetactivescheme.md` (LocalFree), `…-powersetactivescheme.md`, `…-powersettingregisternotification.md` (immediate first callback), `powrprof/ns-powrprof-device_notify_subscribe_parameters.md`, `powrprof/nc-powrprof-device_notify_callback_routine.md`, `powerbase/nf-powerbase-getpwrcapabilities.md`, `winnt/ns-winnt-system_power_capabilities.md`, `winbase/nf-winbase-setthreadexecutionstate.md`, `winbase/nf-winbase-powercreaterequest.md`, `winbase/nf-winbase-powersetrequest.md`, `minwinbase/ns-minwinbase-reason_context.md`, `winbase/ns-winbase-system_power_status.md`, `winuser/nf-winuser-registerpowersettingnotification.md`, `winuser/ns-winuser-powerbroadcast_setting.md`, `winuser/nf-winuser-exitwindowsex.md`, `winuser/nf-winuser-lockworkstation.md`, `winreg/nf-winreg-initiatesystemshutdownexw.md`, `winreg/nf-winreg-abortsystemshutdownw.md`, `shellapi/nf-shellapi-shellexecutew.md`, `shellapi/nf-shellapi-shellexecuteexw.md`, `shellapi/ns-shellapi-shellexecuteinfow.md`, `securitybaseapi/nf-securitybaseapi-checktokenmembership.md`, `winnt/ne-winnt-system_power_condition.md` — https://github.com/MicrosoftDocs/sdk-api
- `MicrosoftDocs/win32` (`desktop-src/`): `Power/power-setting-guids.md`, `Power/wm-powerbroadcast.md`, `Power/pbt-powersettingchange.md`, `Power/system-power-states.md` (Modern Standby, hibernation), `menurc/wm-syscommand.md`, `Shutdown/how-to-shut-down-the-system.md` — https://github.com/MicrosoftDocs/win32
- `MicrosoftDocs/windowsserverdocs`: `WindowsServerDocs/administration/windows-commands/shutdown.md`, `schtasks-create.md` — https://github.com/MicrosoftDocs/windowsserverdocs
- `golang/sys` v0.33.0 `windows/{zsyscall_windows,security_windows,syscall_windows,types_windows,zerrors_windows}.go`; `golang/go` `src/syscall/exec_windows.go` (`SysProcAttr.HideWindow` → `STARTF_USESHOWWINDOW/SW_HIDE`)
- Official pages consulted via search snippets only: https://learn.microsoft.com/windows-hardware/design/device-experiences/powercfg-command-line-options ; https://learn.microsoft.com/en-us/windows-hardware/customize/power-settings/power-button-and-lid-settings (lid open wake 0/1; button/lid indexes) ; https://learn.microsoft.com/en-us/answers/questions/315875/powercfg-to-get-ac-index-value
- Standard users may change power settings (UAC design): https://technet.microsoft.com/da-dk/magazine/2006.11.uac ; https://msdn2.microsoft.com/en-us/library/bb963893(v=msdn.10)
- Modern Standby vs `SetSuspendState`: https://community.osr.com/discussion/comment/304430 ; https://www.bleepingcomputer.com/forums/t/771743/sleep-shortcut-not-working ; https://learn.microsoft.com/en-us/answers/questions/2120673/sleep-from-command-line-with-psshutdown-no-longer
- `powercfg /h off` needs elevation (0x65 / access denied reports): https://www.windows-faq.de/2018/08/28/es-ist-ein-unerwarteter-fehler-0x65-aufgetreten/ ; https://www.techrepublic.com/forums/discussions/hiberfilsys-directory/
- `SC_MONITORPOWER` broadcast usage: https://static.codeproject.com/KB/system/display_states.aspx ; https://learn.microsoft.com/en-za/answers/questions/895451/shut-off-monitor-will-flash-display-by-use-sendmes
