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
