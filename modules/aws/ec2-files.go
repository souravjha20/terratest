package aws

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gruntwork-io/terratest/modules/customerrors"
	"github.com/gruntwork-io/terratest/modules/files"
	"github.com/gruntwork-io/terratest/modules/ssh"
)

type RemoteFileSpecification struct {
	AsgNames               []string            //ASGs where our instances will be
	RemotePathToFileFilter map[string][]string //A map of the files to fetch, where the keys are directories on the remote host and the values are filters for what files to fetch from the directory. The filters support bash-style wildcards.
	UseSudo                bool
	SshUser                string
	SshAuth                *SshAuth
	LocalDestinationDir    string //base path where to store downloaded artifacts locally. The final path of each resource will include the ip of the host and the name of the immediate parent folder.
}

// Specify one of KeyPair, SshAgent, or OverrideSshAgent should be specified
// Any more (or less) than one will result in error on use
type SshAuth struct {
	// EC2 Key Pair
	KeyPair *Ec2Keypair
	// If true, use default SSH agent on local system (started externally, available at SSH_AUTH_SOCK)
	SshAgent bool
	// Override SSH agent on local system
	OverrideSshAgent *ssh.SshAgent

	// The enabled authentication method
	// Internally will be one of "keypair", "sshagent", or "overridesshagent"
	enabledAuthMethod string
}

func (s *SshAuth) Validate() error {
	if s.KeyPair == nil && s.SshAgent == false && s.OverrideSshAgent == nil {
		return fmt.Errorf("One of KeyPair, SshAgent or OverrideSshAgent must be set for SshAuth struct")
	}
	multipleError := fmt.Errorf("Only one of KeyPair, SshAgent or OverrideSshAgent should be specified in SshAuth struct")
	if s.KeyPair != nil {
		if s.SshAgent != false || s.OverrideSshAgent != nil {
			return multipleError
		}
		s.enabledAuthMethod = "keypair"
		return nil
	}
	if s.SshAgent == true {
		if s.KeyPair != nil || s.OverrideSshAgent != nil {
			return multipleError
		}
		s.enabledAuthMethod = "sshagent"
		return nil
	}
	if s.OverrideSshAgent != nil {
		if s.KeyPair != nil || s.SshAgent != false {
			return multipleError
		}
		s.enabledAuthMethod = "overridesshagent"
		return nil
	}
	return fmt.Errorf("Unexpected error validating SshAuth struct")
}

// Attaches the correct authentication method to an ssh.Host struct instance
func addAuthToSshHost(t *testing.T, sshHost *ssh.Host, sshAuth *SshAuth) {
	// Assume SshAuth input is already validated
	switch sshAuth.enabledAuthMethod {
	case "keypair":
		// TODOLATER: cleaner approach to propagate the ssh.KeyPair fields only?
		useKeyPair := ssh.KeyPair{
			PublicKey:  sshAuth.KeyPair.PublicKey,
			PrivateKey: sshAuth.KeyPair.PrivateKey,
		}
		sshHost.SshKeyPair = &useKeyPair
	case "sshagent":
		sshHost.SshAgent = true
	case "overridesshagent":
		sshHost.OverrideSshAgent = sshAuth.OverrideSshAgent
	default:
		t.Fatalf("Invalid enabled auth method '%s'\n", sshAuth.enabledAuthMethod)
	}
}

// FetchContentsOfFileFromInstance looks up the public IP address of the EC2 Instance with the given ID, connects to
// the Instance via SSH using the given username and one of: Key Pair, SSH Agent or Override SSH Agent auth methods,
// fetches the contents of the file at the given path (using sudo if useSudo is true), and returns the contents of
// that file as a string.
func FetchContentsOfFileFromInstance(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, instanceID string, useSudo bool, filePath string) string {
	out, err := FetchContentsOfFileFromInstanceE(t, awsRegion, sshUserName, sshAuth, instanceID, useSudo, filePath)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// FetchContentsOfFileFromInstanceE looks up the public IP address of the EC2 Instance with the given ID, connects to
// the Instance via SSH using the given username and one of: Key Pair, SSH Agent or Override SSH Agent auth methods,
// fetches the contents of the file at the given path (using sudo if useSudo is true), and returns the contents of
// that file as a string.
func FetchContentsOfFileFromInstanceE(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, instanceID string, useSudo bool, filePath string) (string, error) {
	publicIp, err := GetPublicIpOfEc2InstanceE(t, instanceID, awsRegion)
	if err != nil {
		return "", err
	}

	host := ssh.Host{
		SshUserName: sshUserName,
		Hostname:    publicIp,
	}
	addAuthToSshHost(t, &host, sshAuth)

	return ssh.FetchContentsOfFileE(t, host, useSudo, filePath)
}

// FetchContentsOfFilesFromInstance looks up the public IP address of the EC2 Instance with the given ID, connects to
// the Instance via SSH using the given username and one of: Key Pair, SSH Agent or Override SSH Agent auth methods,
// fetches the contents of the files at the given paths (using sudo if useSudo is true), and returns a map from file path
// to the contents of that file as a string.
func FetchContentsOfFilesFromInstance(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, instanceID string, useSudo bool, filePaths ...string) map[string]string {
	out, err := FetchContentsOfFilesFromInstanceE(t, awsRegion, sshUserName, sshAuth, instanceID, useSudo, filePaths...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// FetchContentsOfFilesFromInstanceE looks up the public IP address of the EC2 Instance with the given ID, connects to
// the Instance via SSH using the given username and one of: Key Pair, SSH Agent or Override SSH Agent auth methods,
// fetches the contents of the files at the given paths (using sudo if useSudo is true), and returns a map from file path
// to the contents of that file as a string.
func FetchContentsOfFilesFromInstanceE(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, instanceID string, useSudo bool, filePaths ...string) (map[string]string, error) {
	publicIp, err := GetPublicIpOfEc2InstanceE(t, instanceID, awsRegion)
	if err != nil {
		return nil, err
	}

	host := ssh.Host{
		SshUserName: sshUserName,
		Hostname:    publicIp,
	}
	addAuthToSshHost(t, &host, sshAuth)

	return ssh.FetchContentsOfFilesE(t, host, useSudo, filePaths...)
}

// FetchContentsOfFileFromAsg looks up the EC2 Instances in the given ASG, looks up the public IPs of those EC2
// Instances, connects to each Instance via SSH using the given username and one of: Key Pair, SSH Agent or
// Override SSH Agent auth methods, fetches the contents of the file at the given path (using sudo if useSudo is true),
// and returns a map from Instance ID to the contents of that file as a string.
func FetchContentsOfFileFromAsg(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, asgName string, useSudo bool, filePath string) map[string]string {
	out, err := FetchContentsOfFileFromAsgE(t, awsRegion, sshUserName, sshAuth, asgName, useSudo, filePath)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// FetchContentsOfFileFromAsgE looks up the EC2 Instances in the given ASG, looks up the public IPs of those EC2
// Instances, connects to each Instance via SSH using the given username and one of: Key Pair, SSH Agent or
// Override SSH Agent auth methods, fetches the contents of the file at the given path (using sudo if useSudo is true),
// and returns a map from Instance ID to the contents of that file as a string.
func FetchContentsOfFileFromAsgE(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, asgName string, useSudo bool, filePath string) (map[string]string, error) {
	instanceIDs, err := GetInstanceIdsForAsgE(t, asgName, awsRegion)
	if err != nil {
		return nil, err
	}

	instanceIdToContents := map[string]string{}

	for _, instanceID := range instanceIDs {
		contents, err := FetchContentsOfFileFromInstanceE(t, awsRegion, sshUserName, sshAuth, instanceID, useSudo, filePath)
		if err != nil {
			return nil, err
		}
		instanceIdToContents[instanceID] = contents
	}

	return instanceIdToContents, err
}

// FetchContentsOfFilesFromAsg looks up the EC2 Instances in the given ASG, looks up the public IPs of those EC2
// Instances, connects to each Instance via SSH using the given username and one of: Key Pair, SSH Agent or
// Override SSH Agent auth methods, fetches the contents of the files at the given paths (using sudo if useSudo is true),
// and returns a map from Instance ID to a map of file path to the contents of that file as a string.
func FetchContentsOfFilesFromAsg(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, asgName string, useSudo bool, filePaths ...string) map[string]map[string]string {
	out, err := FetchContentsOfFilesFromAsgE(t, awsRegion, sshUserName, sshAuth, asgName, useSudo, filePaths...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// FetchContentsOfFilesFromAsgE looks up the EC2 Instances in the given ASG, looks up the public IPs of those EC2
// Instances, connects to each Instance via SSH using the given username and one of: Key Pair, SSH Agent or
// Override SSH Agent auth methods, fetches the contents of the files at the given paths (using sudo if useSudo is true),
// and returns a map from Instance ID to a map of file path to the contents of that file as a string.
func FetchContentsOfFilesFromAsgE(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, asgName string, useSudo bool, filePaths ...string) (map[string]map[string]string, error) {
	instanceIDs, err := GetInstanceIdsForAsgE(t, asgName, awsRegion)
	if err != nil {
		return nil, err
	}

	instanceIdToFilePathToContents := map[string]map[string]string{}

	for _, instanceID := range instanceIDs {
		contents, err := FetchContentsOfFilesFromInstanceE(t, awsRegion, sshUserName, sshAuth, instanceID, useSudo, filePaths...)
		if err != nil {
			return nil, err
		}
		instanceIdToFilePathToContents[instanceID] = contents
	}

	return instanceIdToFilePathToContents, err
}

// FetchFilesFromInstance looks up the EC2 Instances in the given ASG, looks up the public IPs of those EC2
// Instances, connects to each Instance via SSH using the given username and one of: Key Pair, SSH Agent or
// Override SSH Agent auth methods, downloads the files matching filenameFilters at the given remoteDirectory
// (using sudo if useSudo is true), and stores the files locally at localDirectory/<publicip>/<remoteFolderName>
func FetchFilesFromInstance(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, instanceID string, useSudo bool, remoteDirectory string, localDirectory string, filenameFilters []string) {
	err := FetchFilesFromInstanceE(t, awsRegion, sshUserName, sshAuth, instanceID, useSudo, remoteDirectory, localDirectory, filenameFilters)

	if err != nil {
		t.Fatal(err)
	}
}

// FetchFilesFromInstanceE looks up the EC2 Instances in the given ASG, looks up the public IPs of those EC2
// Instances, connects to each Instance via SSH using the given username and one of: Key Pair, SSH Agent or
// Override SSH Agent auth methods, downloads the files matching filenameFilters at the given remoteDirectory
// (using sudo if useSudo is true), and stores the files locally at localDirectory/<publicip>/<remoteFolderName>
func FetchFilesFromInstanceE(t *testing.T, awsRegion string, sshUserName string, sshAuth *SshAuth, instanceID string, useSudo bool, remoteDirectory string, localDirectory string, filenameFilters []string) error {
	publicIp, err := GetPublicIpOfEc2InstanceE(t, instanceID, awsRegion)

	if err != nil {
		return err
	}

	// TODO: adjust for SSH auth methods
	host := ssh.Host{
		Hostname:    publicIp,
		SshUserName: sshUserName,
	}
	addAuthToSshHost(t, &host, sshAuth)

	finalLocalDestDir := filepath.Join(localDirectory, publicIp, filepath.Base(remoteDirectory))

	if !files.FileExists(finalLocalDestDir) {
		os.MkdirAll(finalLocalDestDir, 0755)
	}

	scpOptions := ssh.ScpDownloadOptions{
		RemoteHost:      host,
		RemoteDir:       remoteDirectory,
		LocalDir:        finalLocalDestDir,
		FileNameFilters: filenameFilters,
	}

	return ssh.ScpDirFromE(t, scpOptions, useSudo)
}

// FetchFilesFromAsgs looks up the EC2 Instances in all the ASGs given in the RemoteFileSpecification,
// looks up the public IPs of those EC2 Instances, connects to each Instance via SSH using the given
// username and one of: Key Pair, SSH Agent or Override SSH Agent auth methods, downloads the files
// matching filenameFilters at the given remoteDirectory (using sudo if useSudo is true), and stores
// the files locally at localDirectory/<publicip>/<remoteFolderName>
func FetchFilesFromAsgs(t *testing.T, awsRegion string, spec RemoteFileSpecification) {
	err := FetchFilesFromAsgsE(t, awsRegion, spec)

	if err != nil {
		t.Fatal(err)
	}
}

// FetchFilesFromAsgsE looks up the EC2 Instances in all the ASGs given in the RemoteFileSpecification,
// looks up the public IPs of those EC2 Instances, connects to each Instance via SSH using the given
// username and one of: Key Pair, SSH Agent or Override SSH Agent auth methods, downloads the files
// matching filenameFilters at the given remoteDirectory (using sudo if useSudo is true), and stores
// the files locally at localDirectory/<publicip>/<remoteFolderName>
func FetchFilesFromAsgsE(t *testing.T, awsRegion string, spec RemoteFileSpecification) error {
	errorsOccurred := []error{}

	for _, curAsg := range spec.AsgNames {
		for curRemoteDir, fileFilters := range spec.RemotePathToFileFilter {

			instanceIDs, err := GetInstanceIdsForAsgE(t, curAsg, awsRegion)
			if err != nil {
				errorsOccurred = append(errorsOccurred, err)
			} else {
				for _, instanceID := range instanceIDs {
					err = FetchFilesFromInstanceE(t, awsRegion, spec.SshUser, spec.SshAuth, instanceID, spec.UseSudo, curRemoteDir, spec.LocalDestinationDir, fileFilters)

					if err != nil {
						errorsOccurred = append(errorsOccurred, err)
					}
				}
			}
		}
	}
	return customerrors.NewMultiError(errorsOccurred...)
}
