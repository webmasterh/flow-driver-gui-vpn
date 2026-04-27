package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type VPNManager struct {
	cmd           *exec.Cmd
	isConnected   bool
	mu            sync.Mutex
	statusLabel   *widget.Label
	logText       *widget.Entry
	connectBtn    *widget.Button
	disconnectBtn *widget.Button
	setProxyBtn   *widget.Button
	clearProxyBtn *widget.Button
	clientDir     string
	dirEntry      *widget.Entry
	window        fyne.Window
}

func NewVPNManager(window fyne.Window) *VPNManager {
	// exePath, _ := os.Executable()
	// defaultDir := filepath.Dir(exePath)
	defaultDir := "./client"
	
	return &VPNManager{
		isConnected: false,
		clientDir:   defaultDir,
		window:      window,
	}
}

func (vm *VPNManager) validateFiles() error {
	clientPath := filepath.Join(vm.clientDir, "client.exe")
	configPath := filepath.Join(vm.clientDir, "client_config.json")
	credPath := filepath.Join(vm.clientDir, "credentials.json")

	if _, err := os.Stat(clientPath); os.IsNotExist(err) {
		return fmt.Errorf("client.exe not found in %s", vm.clientDir)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("client_config.json not found in %s", vm.clientDir)
	}
	if _, err := os.Stat(credPath); os.IsNotExist(err) {
		return fmt.Errorf("credentials.json not found in %s", vm.clientDir)
	}
	
	return nil
}

func (vm *VPNManager) startVPN() error {
	vm.mu.Lock()
	if vm.isConnected {
		vm.mu.Unlock()
		return fmt.Errorf("VPN is already running")
	}
	vm.mu.Unlock()

	vm.appendLog("Validating files...")
	if err := vm.validateFiles(); err != nil {
		vm.appendLog("Validation failed: " + err.Error())
		return err
	}
	vm.appendLog("Files validated successfully")

	clientPath := filepath.Join(vm.clientDir, "client.exe")
	configPath := filepath.Join(vm.clientDir, "client_config.json")
	credPath := filepath.Join(vm.clientDir, "credentials.json")

	vm.appendLog("Starting VPN client...")
	vm.appendLog(fmt.Sprintf("Command: %s -c %s -gc %s", clientPath, configPath, credPath))

	cmd := exec.Command(clientPath, "-c", configPath, "-gc", credPath)
	
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		vm.appendLog("Failed to create stdout pipe: " + err.Error())
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		vm.appendLog("Failed to create stderr pipe: " + err.Error())
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		vm.appendLog("Failed to start VPN: " + err.Error())
		return fmt.Errorf("failed to start VPN: %v", err)
	}

	vm.mu.Lock()
	vm.cmd = cmd
	vm.mu.Unlock()

	vm.appendLog("VPN process started (PID: " + fmt.Sprint(cmd.Process.Pid) + ")")
	vm.updateStatus("Starting...", false)

	go vm.readOutput(stdout, "OUT")
	go vm.readOutput(stderr, "ERR")

	go func() {
		err := cmd.Wait()
		vm.mu.Lock()
		vm.isConnected = false
		vm.cmd = nil
		vm.mu.Unlock()

		if err != nil {
			vm.appendLog("VPN process exited with error: " + err.Error())
		} else {
			vm.appendLog("VPN process exited normally")
		}
		vm.updateStatus("Disconnected", false)
	}()

	return nil
}

func (vm *VPNManager) readOutput(pipe io.ReadCloser, prefix string) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		vm.appendLog(fmt.Sprintf("[%s] %s", prefix, line))

		if strings.Contains(line, "Listening for SOCKS5") {
			vm.mu.Lock()
			vm.isConnected = true
			vm.mu.Unlock()
			vm.updateStatus("Connected", true)
		}
		
		if strings.Contains(line, "Starting Flow Client") {
			vm.updateStatus("Connecting...", false)
		}
	}
	
	if err := scanner.Err(); err != nil {
		vm.appendLog(fmt.Sprintf("[%s] Error reading output: %s", prefix, err.Error()))
	}
}

func (vm *VPNManager) stopVPN() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.cmd == nil || !vm.isConnected {
		return fmt.Errorf("VPN is not running")
	}

	if err := vm.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to stop VPN: %v", err)
	}

	vm.isConnected = false
	vm.cmd = nil
	return nil
}

func (vm *VPNManager) appendLog(text string) {
	timestamp := time.Now().Format("15:04:05")
	logLine := fmt.Sprintf("[%s] %s\n", timestamp, text)
	currentText := vm.logText.Text
	vm.logText.SetText(currentText + logLine)
	
	// Auto-scroll to bottom by moving cursor to end
	vm.logText.CursorRow = len(strings.Split(vm.logText.Text, "\n"))
}

func (vm *VPNManager) updateStatus(status string, connected bool) {
	vm.statusLabel.SetText("Status: " + status)
	if connected {
		vm.connectBtn.Disable()
		vm.disconnectBtn.Enable()
	} else {
		vm.connectBtn.Enable()
		vm.disconnectBtn.Disable()
	}
	vm.connectBtn.Refresh()
	vm.disconnectBtn.Refresh()
}

func (vm *VPNManager) loadConfig() (string, error) {
	configPath := filepath.Join(vm.clientDir, "client_config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}

	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return string(data), nil
	}

	prettyJSON, err := json.MarshalIndent(jsonData, "", "  ")
	if err != nil {
		return string(data), nil
	}

	return string(prettyJSON), nil
}

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("Flow Driver GUI")

	vm := NewVPNManager(myWindow)

	// Status label
	vm.statusLabel = widget.NewLabel("Status: Disconnected")

	// Connect button
	vm.connectBtn = widget.NewButton("Start VPN", func() {
		if err := vm.startVPN(); err != nil {
			vm.appendLog(fmt.Sprintf("Error: %v", err))
			dialog.ShowError(err, myWindow)
		}
	})

	// Disconnect button
	vm.disconnectBtn = widget.NewButton("Stop VPN", func() {
		if err := vm.stopVPN(); err != nil {
			vm.appendLog(fmt.Sprintf("Error: %v", err))
		} else {
			vm.appendLog("VPN stopped")
		}
	})

	vm.setProxyBtn = widget.NewButton("Set Proxy", func() {
		if err := setSystemProxy("127.0.0.1:1080"); err != nil {
			vm.appendLog(fmt.Sprintf("Error setting proxy: %v", err))
			dialog.ShowError(err, myWindow)
		} else {
			vm.appendLog("Proxy set successfully")
		}
	})

	vm.clearProxyBtn = widget.NewButton("Clear Proxy", func() {
		if err := clearSystemProxy(); err != nil {
			vm.appendLog(fmt.Sprintf("Error clearing proxy: %v", err))
			dialog.ShowError(err, myWindow)
		} else {
			vm.appendLog("Proxy cleared successfully")
		}
	})

	vm.disconnectBtn.Disable()

	// Log display with scroll container
	vm.logText = widget.NewMultiLineEntry()
	vm.logText.SetPlaceHolder("VPN logs will appear here...")
	vm.logText.Wrapping = fyne.TextWrapWord
	
	logScroll := container.NewScroll(vm.logText)
	logScroll.SetMinSize(fyne.NewSize(550, 250))

	// Config display
	configText := widget.NewMultiLineEntry()
	configText.Wrapping = fyne.TextWrapWord
	
	refreshConfig := func() {
		configContent, err := vm.loadConfig()
		if err != nil {
			configText.SetText(fmt.Sprintf("Error loading config: %v", err))
		} else {
			configText.SetText(configContent)
		}
	}
	refreshConfig()

	// Settings tab
	vm.dirEntry = widget.NewEntry()
	vm.dirEntry.SetText(vm.clientDir)
	
	browseBtn := widget.NewButton("Browse...", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			vm.clientDir = uri.Path()
			vm.dirEntry.SetText(vm.clientDir)
			refreshConfig()
		}, myWindow)
	})

	validateBtn := widget.NewButton("Validate Files", func() {
		if err := vm.validateFiles(); err != nil {
			dialog.ShowError(err, myWindow)
		} else {
			dialog.ShowInformation("Success", "All required files found!", myWindow)
		}
	})

	saveBtn := widget.NewButton("Save Directory", func() {
		vm.clientDir = vm.dirEntry.Text
		refreshConfig()
		dialog.ShowInformation("Saved", "Client directory updated", myWindow)
	})

	settingsContent := container.NewVBox(
		widget.NewLabel("VPN Client Directory:"),
		vm.dirEntry,
		container.NewHBox(browseBtn, validateBtn, saveBtn),
		widget.NewLabel("\nRequired files:"),
		widget.NewLabel("• client.exe"),
		widget.NewLabel("• client_config.json"),
		widget.NewLabel("• credentials.json"),
	)

	// Tabs
	tabs := container.NewAppTabs(
		container.NewTabItem("Control", container.NewVBox(
			vm.statusLabel,
			container.NewHBox(vm.connectBtn, vm.disconnectBtn),
			container.NewHBox(vm.setProxyBtn, vm.clearProxyBtn),
			widget.NewLabel("Logs:"),
			logScroll,
		)),
		container.NewTabItem("Configuration", container.NewScroll(configText)),
		container.NewTabItem("Settings", settingsContent),
	)

	myWindow.SetContent(tabs)
	myWindow.Resize(fyne.NewSize(650, 500))
	myWindow.ShowAndRun()
}

// System proxy management functions
func setSystemProxy(proxyURL string) error {
	switch runtime.GOOS {
	case "windows":
		return setWindowsProxy(proxyURL)
	case "darwin": // macOS
		return setMacOSProxy(proxyURL)
	case "linux":
		return setLinuxProxy(proxyURL)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func clearSystemProxy() error {
	switch runtime.GOOS {
	case "windows":
		return clearWindowsProxy()
	case "darwin": // macOS
		return clearMacOSProxy()
	case "linux":
		return clearLinuxProxy()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func setWindowsProxy(proxyURL string) error {
	// Set proxy server
	cmd := exec.Command("reg", "add", 
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"/v", "ProxyServer", "/t", "REG_SZ", "/d", proxyURL, "/f")
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set Windows proxy: %s - %v", string(output), err)
	}
	
	// Enable proxy
	cmd = exec.Command("reg", "add",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to enable Windows proxy: %s - %v", string(output), err)
	}
	
	// Set proxy override for local addresses
	cmd = exec.Command("reg", "add",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"/v", "ProxyOverride", "/t", "REG_SZ", "/d", "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*", "/f")
	
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set proxy override: %s - %v", string(output), err)
	}
	
	return nil
}

func clearWindowsProxy() error {
	// Disable proxy
	cmd := exec.Command("reg", "add",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to disable Windows proxy: %s - %v", string(output), err)
	}
	
	// Clear proxy server
	cmd = exec.Command("reg", "delete",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"/v", "ProxyServer", "/f")
	
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clear Windows proxy server: %s - %v", string(output), err)
	}
	
	// Clear proxy override
	cmd = exec.Command("reg", "delete",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"/v", "ProxyOverride", "/f")
	
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clear proxy override: %s - %v", string(output), err)
	}
	
	return nil
}

func setMacOSProxy(proxyURL string) error {
	// Get current network interface
	cmd := exec.Command("networksetup", "-listallhardwareports")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get network interfaces: %s - %v", string(output), err)
	}
	
	// Find Wi-Fi interface (common on macOS)
	interfaceName := ""
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Wi-Fi") || strings.Contains(line, "AirPort") {
			if i+1 < len(lines) {
				interfaceName = strings.TrimSpace(strings.TrimPrefix(lines[i+1], "Hardware Port: "))
				break
			}
		}
	}
	
	if interfaceName == "" {
		// Try to get the first available interface
		for _, line := range lines {
			if strings.HasPrefix(line, "Device:") {
				interfaceName = strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
				break
			}
		}
	}
	
	if interfaceName != "" && interfaceName != "USB" {
		cmd = exec.Command("networksetup", "-setproxyserver", interfaceName, proxyURL)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to set macOS proxy: %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setautoproxystate", interfaceName, "off")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to disable auto proxy on macOS: %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setwebproxy", interfaceName, "127.0.0.1", "1080")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to set web proxy on macOS: %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setsecurewebproxy", interfaceName, "127.0.0.1", "1080")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to set secure web proxy on macOS: %s - %v", string(output), err)
		}
		
		return nil
	} else {
		cmd = exec.Command("networksetup", "-setproxyserver", "Wi-Fi", proxyURL)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to set macOS proxy (fallback): %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setautoproxystate", "Wi-Fi", "off")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to disable auto proxy on macOS (fallback): %s - %v", string(output), err)
		}
		
		return nil
	}
}

func clearMacOSProxy() error {
	// Get current network interface
	cmd := exec.Command("networksetup", "-listallhardwareports")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get network interfaces: %s - %v", string(output), err)
	}
	
	interfaceName := ""
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Wi-Fi") || strings.Contains(line, "AirPort") {
			if i+1 < len(lines) {
				interfaceName = strings.TrimSpace(strings.TrimPrefix(lines[i+1], "Hardware Port: "))
				break
			}
		}
	}
	
	if interfaceName == "" {
		for _, line := range lines {
			if strings.HasPrefix(line, "Device:") {
				interfaceName = strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
				break
			}
		}
	}
	
	if interfaceName != "" && interfaceName != "USB" {
		cmd = exec.Command("networksetup", "-setproxyserver", interfaceName, "")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to clear macOS proxy: %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setautoproxystate", interfaceName, "off")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to disable auto proxy on macOS: %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setwebproxy", interfaceName, "", "0")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to clear web proxy on macOS: %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setsecurewebproxy", interfaceName, "", "0")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to clear secure web proxy on macOS: %s - %v", string(output), err)
		}
		
		return nil
	} else {
		cmd = exec.Command("networksetup", "-setproxyserver", "Wi-Fi", "")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to clear macOS proxy (fallback): %s - %v", string(output), err)
		}
		
		cmd = exec.Command("networksetup", "-setautoproxystate", "Wi-Fi", "off")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to disable auto proxy on macOS (fallback): %s - %v", string(output), err)
		}
		
		return nil
	}
}

func setLinuxProxy(proxyURL string) error {
	// Set environment variables for current session
	os.Setenv("HTTP_PROXY", "http://"+proxyURL)
	os.Setenv("HTTPS_PROXY", "http://"+proxyURL)
	os.Setenv("http_proxy", "http://"+proxyURL)
	os.Setenv("https_proxy", "http://"+proxyURL)
	
	// For desktop environments, set system-wide proxy
	cmd := exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "host", strings.Split(proxyURL, ":")[0])
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set GNOME HTTP proxy: %s - %v", string(output), err)
	}
	
	cmd = exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "port", strings.Split(proxyURL, ":")[1])
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set GNOME HTTP proxy port: %s - %v", string(output), err)
	}
	
	cmd = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "host", strings.Split(proxyURL, ":")[0])
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set GNOME HTTPS proxy: %s - %v", string(output), err)
	}
	
	cmd = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "port", strings.Split(proxyURL, ":")[1])
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set GNOME HTTPS proxy port: %s - %v", string(output), err)
	}
	
	cmd = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "manual")
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to enable GNOME proxy mode: %s - %v", string(output), err)
	}
	
	return nil
}

func clearLinuxProxy() error {
	// Clear environment variables
	os.Unsetenv("HTTP_PROXY")
	os.Unsetenv("HTTPS_PROXY")
	os.Unsetenv("http_proxy")
	os.Unsetenv("https_proxy")
	
	// Reset GNOME proxy settings to none
	cmd := exec.Command("gsettings", "set", "org.gnome.system.proxy.http", "host", "")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clear GNOME HTTP proxy: %s - %v", string(output), err)
	}
	
	cmd = exec.Command("gsettings", "set", "org.gnome.system.proxy.https", "host", "")
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clear GNOME HTTPS proxy: %s - %v", string(output), err)
	}
	
	cmd = exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none")
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to disable GNOME proxy mode: %s - %v", string(output), err)
	}
	
	return nil
}
