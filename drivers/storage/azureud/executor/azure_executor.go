package executor

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	log "github.com/Sirupsen/logrus"

	gofig "github.com/akutz/gofig/types"
	"github.com/akutz/goof"
	"github.com/akutz/gotil"

	"github.com/codedellemc/libstorage/api/registry"
	"github.com/codedellemc/libstorage/api/types"
	"github.com/codedellemc/libstorage/drivers/storage/azureud"
	"github.com/codedellemc/libstorage/drivers/storage/azureud/utils"
)

// driver is the storage executor for the azureud storage driver.
type driver struct {
	config gofig.Config
}

func init() {
	registry.RegisterStorageExecutor(azureud.Name, newDriver)
}

func newDriver() types.StorageExecutor {
	return &driver{}
}

func (d *driver) Init(ctx types.Context, config gofig.Config) error {
	ctx.Info("azureud_executor: Init")
	d.config = config
	return nil
}

func (d *driver) Name() string {
	return azureud.Name
}

// Supported returns a flag indicating whether or not the platform
// implementing the executor is valid for the host on which the executor
// resides.
func (d *driver) Supported(
	ctx types.Context,
	opts types.Store) (bool, error) {

	if !gotil.FileExistsInPath("lsscsi") {
		ctx.Error("lsscsi executable not found in PATH")
		return false, nil
	}

	return utils.IsAzureInstance(ctx)
}

// InstanceID returns the instance ID from the current instance from metadata
func (d *driver) InstanceID(
	ctx types.Context,
	opts types.Store) (*types.InstanceID, error) {
	return utils.InstanceID(ctx)
}

var errNoAvaiDevice = goof.New("no available device")
var nextDevRe = regexp.MustCompile("^/dev/" +
	utils.NextDeviceInfo.Prefix +
	"(" + utils.NextDeviceInfo.Pattern + ")")
var availLetters = []string{
	"c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n",
	"o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}

// NextDevice returns the next available device.
func (d *driver) NextDevice(
	ctx types.Context,
	opts types.Store) (string, error) {
	// All possible device paths on Linux instances are /dev/sd[c-z]

	// Find which letters are used for local devices
	localDeviceNames := make(map[string]bool)

	localDevices, err := d.LocalDevices(
		ctx, &types.LocalDevicesOpts{Opts: opts})
	if err != nil {
		return "", goof.WithError("error getting local devices", err)
	}
	localDeviceMapping := localDevices.DeviceMap

	for localDevice := range localDeviceMapping {
		res := nextDevRe.FindStringSubmatch(localDevice)
		if len(res) > 0 {
			localDeviceNames[res[1]] = true
		}
	}

	// Find next available letter for device path
	for _, letter := range availLetters {
		if localDeviceNames[letter] {
			continue
		}
		return fmt.Sprintf(
			"/dev/%s%s", utils.NextDeviceInfo.Prefix, letter), nil
	}
	return "", errNoAvaiDevice
}

var (
	devRX  = regexp.MustCompile(`^/dev/sd[c-z]$`)
	scsiRx = regexp.MustCompile(`^\[\d+:\d+:\d+:(\d+)\]$`)
)

// Retrieve device paths currently attached and/or mounted
func (d *driver) LocalDevices(
	ctx types.Context,
	opts *types.LocalDevicesOpts) (*types.LocalDevices, error) {

	// Read all of the attached devices
	scsiDevs, err := getSCSIDevs()
	if err != nil {
		return nil, err
	}

	devMap := map[string]string{}

	scanner := bufio.NewScanner(bytes.NewReader(scsiDevs))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		device := fields[len(fields)-1]
		if !devRX.MatchString(device) {
			continue
		}

		matches := scsiRx.FindStringSubmatch(fields[0])
		if matches == nil {
			continue
		}

		lun := matches[1]
		devMap[device] = lun
	}

	ld := &types.LocalDevices{Driver: d.Name()}
	if len(devMap) > 0 {
		ld.DeviceMap = devMap
	}

	ctx.WithField("devicemap", ld.DeviceMap).Debug("local devices")

	return ld, nil
}

func getSCSIDevs() ([]byte, error) {

	out, err := exec.Command("lsscsi").Output()
	if err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			stderr := string(exiterr.Stderr)
			log.Errorf("Unable to get scsi devices: %s", stderr)
			return nil,
				goof.Newf("Unable to get scsi devices: %s",
					stderr)
		}
		return nil, goof.WithError("Unable to get scsci devices", err)
	}

	return out, nil
}
