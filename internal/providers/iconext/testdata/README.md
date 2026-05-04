# Icon Extraction Test Fixtures

## sample.exe

A copy of `C:\Windows\System32\notepad.exe` (Windows system binary, redistributable under typical fair-use licensing for test fixtures). Used by `iconext_test.go` to verify icon extraction functionality.

The file contains embedded 32×32 and other-sized icons suitable for testing the Win32 icon extraction pipeline via `ExtractIconExW` and `GetDIBits`.
