$bin = [Environment]::ExpandEnvironmentVariables('%USERPROFILE%\.local\bin')
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$segments = @()
if ($userPath) { $segments = $userPath.Split(';') | Where-Object { $_ -and $_.Trim() -ne '' } }
$normalized = $segments | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\').ToLowerInvariant() }
if (-not ($normalized -contains $bin.TrimEnd('\').ToLowerInvariant())) {
  $newPath = if ([string]::IsNullOrWhiteSpace($userPath)) { '%USERPROFILE%\.local\bin' } else { '%USERPROFILE%\.local\bin;' + $userPath }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  $signature = @'
using System;
using System.Runtime.InteropServices;
public static class NativeMethods {
  [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
  public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, IntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out IntPtr lpdwResult);
}
'@
  Add-Type -TypeDefinition $signature -ErrorAction SilentlyContinue | Out-Null
  $HWND_BROADCAST = [IntPtr]0xffff
  $WM_SETTINGCHANGE = 0x1A
  $SMTO_ABORTIFHUNG = 0x2
  $result = [IntPtr]::Zero
  [void][NativeMethods]::SendMessageTimeout($HWND_BROADCAST, $WM_SETTINGCHANGE, [IntPtr]::Zero, 'Environment', $SMTO_ABORTIFHUNG, 5000, [ref]$result)
}