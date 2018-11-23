package occlient

import (
	taro "archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/fatih/color"
	"github.com/golang/glog"
	dockerapiv10 "github.com/openshift/api/image/docker10"
	"github.com/pkg/errors"
	"github.com/redhat-developer/odo/pkg/config"
	"github.com/redhat-developer/odo/pkg/util"

	servicecatalogclienset "github.com/kubernetes-incubator/service-catalog/pkg/client/clientset_generated/clientset/typed/servicecatalog/v1beta1"
	appsschema "github.com/openshift/client-go/apps/clientset/versioned/scheme"
	appsclientset "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	buildschema "github.com/openshift/client-go/build/clientset/versioned/scheme"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	userclientset "github.com/openshift/client-go/user/clientset/versioned/typed/user/v1"

	scv1beta1 "github.com/kubernetes-incubator/service-catalog/pkg/apis/servicecatalog/v1beta1"
	appsv1 "github.com/openshift/api/apps/v1"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	projectv1 "github.com/openshift/api/project/v1"
	routev1 "github.com/openshift/api/route/v1"
	oauthv1client "github.com/openshift/client-go/oauth/clientset/versioned/typed/oauth/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/retry"
)

// CreateType is an enum to indicate the type of source of component -- local source/binary or git for the generation of app/component names
type CreateType string

const (
	// GIT as source of component
	GIT CreateType = "git"
	// LOCAL Local source path as source of component
	LOCAL CreateType = "local"
	// BINARY Local Binary as source of component
	BINARY CreateType = "binary"
	// NONE indicates there's no information about the type of source of the component
	NONE CreateType = ""
)

// CreateArgs is a container of attributes of component create action
type CreateArgs struct {
	Name            string
	SourcePath      string
	SourceType      CreateType
	ImageName       string
	EnvVars         []string
	Ports           []string
	Resources       []util.ResourceRequirementInfo
	ApplicationName string
}

const (
	ocUpdateTimeout    = 120 * time.Second
	OpenShiftNameSpace = "openshift"

	// The length of the string to be generated for names of resources
	nameLength = 5

	// Image that will be used containing the supervisord binary and assembly scripts
	bootstrapperImage = "quay.io/openshiftdo/supervisord:0.3.0"

	// Create a custom name and (hope) that users don't use the *exact* same name in their deployment
	supervisordVolumeName = "odo-supervisord-shared-data"

	// waitForPodTimeOut controls how long we should wait for a pod before giving up
	waitForPodTimeOut = 120 * time.Second

	// ComponentPortAnnotationName annotation is used on the secrets that are created for each exposed port of the component
	ComponentPortAnnotationName = "component-port"

	// EnvS2IScriptsURL is an env var exposed to https://github.com/redhat-developer/odo-supervisord-image/blob/master/assemble-and-restart to indicate location of s2i scripts in this case assemble script
	EnvS2IScriptsURL = "ODO_S2I_SCRIPTS_URL"

	// EnvS2IScriptsProtocol is an env var exposed to https://github.com/redhat-developer/odo-supervisord-image/blob/master/assemble-and-restart to indicate the way to access location of s2i scripts indicated by ${${EnvS2IScriptsURL}} above
	EnvS2IScriptsProtocol = "ODO_S2I_SCRIPTS_PROTOCOL"

	// EnvS2ISrcOrBinPath is an env var exposed by s2i to indicate where the builder image expects the component source or binary to reside
	EnvS2ISrcOrBinPath = "ODO_S2I_SRC_BIN_PATH"

	// S2IScriptsURLLabel S2I script location Label name
	// Ref: https://docs.openshift.com/enterprise/3.2/creating_images/s2i.html#build-process
	S2IScriptsURLLabel = "io.openshift.s2i.scripts-url"

	// S2ISrcOrBinLabel is the label that is provides, path where S2I expects component source or binary
	S2ISrcOrBinLabel = "io.openshift.s2i.destination"

	// EnvS2IDeploymentDir is an env var exposed to https://github.com/redhat-developer/odo-supervisord-image/blob/master/assemble-and-restart to indicate s2i deployment directory
	EnvS2IDeploymentDir = "ODO_S2I_DEPLOYMENT_DIR"

	// DefaultS2ISrcOrBinPath is the default path where S2I expects source/binary artifacts in absence of $S2ISrcOrBinLabel in builder image
	// Ref: https://github.com/openshift/source-to-image/blob/master/docs/builder_image.md#required-image-contents
	DefaultS2ISrcOrBinPath = "/tmp"
)

// S2IPaths is a struct that will hold path to S2I scripts and the protocol indicating access to them, component source/binary paths, artifacts deployments directory
// These are passed as env vars to component pod
type S2IPaths struct {
	ScriptsPathProtocol string
	ScriptsPath         string
	SrcOrBinPath        string
	DeploymentDir       string
}

// S2IDeploymentsDir is a set of possible S2I labels that provides S2I deployments directory
// This label is not uniform across different builder images. This slice is expected to grow as odo adds support to more component types and/or the respective builder images use different labels
var S2IDeploymentsDir = []string{
	"com.redhat.deployments-dir",
	"org.jboss.deployments-dir",
}

// errorMsg is the message for user when invalid configuration error occurs
const errorMsg = `
Please login to your server: 

odo login https://mycluster.mydomain.com
`

type Client struct {
	kubeClient           kubernetes.Interface
	imageClient          imageclientset.ImageV1Interface
	appsClient           appsclientset.AppsV1Interface
	buildClient          buildclientset.BuildV1Interface
	projectClient        projectclientset.ProjectV1Interface
	serviceCatalogClient servicecatalogclienset.ServicecatalogV1beta1Interface
	routeClient          routeclientset.RouteV1Interface
	userClient           userclientset.UserV1Interface
	KubeConfig           clientcmd.ClientConfig
	Namespace            string
}

func New(connectionCheck bool) (*Client, error) {
	var client Client

	// initialize client-go clients
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	client.KubeConfig = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := client.KubeConfig.ClientConfig()
	if err != nil {
		return nil, errors.New(err.Error() + errorMsg)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.kubeClient = kubeClient

	imageClient, err := imageclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.imageClient = imageClient

	appsClient, err := appsclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.appsClient = appsClient

	buildClient, err := buildclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.buildClient = buildClient

	serviceCatalogClient, err := servicecatalogclienset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.serviceCatalogClient = serviceCatalogClient

	projectClient, err := projectclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.projectClient = projectClient

	routeClient, err := routeclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.routeClient = routeClient

	userClient, err := userclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	client.userClient = userClient

	namespace, _, err := client.KubeConfig.Namespace()
	if err != nil {
		return nil, err
	}
	client.Namespace = namespace

	// Skip this if connectionCheck is false
	if !connectionCheck {
		if !isServerUp(config.Host) {
			return nil, errors.New("Unable to connect to OpenShift cluster, is it down?")
		}
		if !client.isLoggedIn() {
			return nil, errors.New("Please log in to the cluster")
		}
	}
	return &client, nil
}

// parseImageName parse image reference
// returns (imageNamespace, imageName, tag, digest, error)
// if image is referenced by tag (name:tag)  than digest is ""
// if image is referenced by digest (name@digest) than  tag is ""
func ParseImageName(image string) (string, string, string, string, error) {
	digestParts := strings.Split(image, "@")
	if len(digestParts) == 2 {
		// image is references digest
		// Safe path image name and digest are non empty, else error
		if digestParts[0] != "" && digestParts[1] != "" {
			// Image name might be fully qualified name of form: Namespace/ImageName
			imangeNameParts := strings.Split(digestParts[0], "/")
			if len(imangeNameParts) == 2 {
				return imangeNameParts[0], imangeNameParts[1], "", digestParts[1], nil
			}
			return "", imangeNameParts[0], "", digestParts[1], nil
		}
	} else if len(digestParts) == 1 && digestParts[0] != "" { // Filter out empty image name
		tagParts := strings.Split(image, ":")
		if len(tagParts) == 2 {
			// ":1.0.0 is invalid image name"
			if tagParts[0] != "" {
				// Image name might be fully qualified name of form: Namespace/ImageName
				imangeNameParts := strings.Split(tagParts[0], "/")
				if len(imangeNameParts) == 2 {
					return imangeNameParts[0], imangeNameParts[1], tagParts[1], "", nil
				}
				return "", tagParts[0], tagParts[1], "", nil
			}
		} else if len(tagParts) == 1 {
			// Image name might be fully qualified name of form: Namespace/ImageName
			imangeNameParts := strings.Split(tagParts[0], "/")
			if len(imangeNameParts) == 2 {
				return imangeNameParts[0], imangeNameParts[1], "latest", "", nil
			}
			return "", tagParts[0], "latest", "", nil
		}
	}
	return "", "", "", "", fmt.Errorf("invalid image reference %s", image)

}

// imageWithMetadata mutates the given image. It parses raw DockerImageManifest data stored in the image and
// fills its DockerImageMetadata and other fields.
// Copied from v3.7 github.com/openshift/origin/pkg/image/apis/image/v1/helpers.go
func imageWithMetadata(image *imagev1.Image) error {
	// Check if the metadata are already filled in for this image.
	meta, hasMetadata := image.DockerImageMetadata.Object.(*dockerapiv10.DockerImage)
	if hasMetadata && meta.Size > 0 {
		return nil
	}

	version := image.DockerImageMetadataVersion
	if len(version) == 0 {
		version = "1.0"
	}

	obj := &dockerapiv10.DockerImage{}
	if len(image.DockerImageMetadata.Raw) != 0 {
		if err := json.Unmarshal(image.DockerImageMetadata.Raw, obj); err != nil {
			return err
		}
		image.DockerImageMetadata.Object = obj
	}

	image.DockerImageMetadataVersion = version

	return nil
}

// isLoggedIn checks whether user is logged in or not and returns boolean output
func (c *Client) isLoggedIn() bool {
	// ~ indicates current user
	// Reference: https://github.com/openshift/origin/blob/master/pkg/oc/cli/cmd/whoami.go#L55
	output, err := c.userClient.Users().Get("~", metav1.GetOptions{})
	glog.V(4).Infof("isLoggedIn err:  %#v \n output: %#v", err, output.Name)
	if err != nil {
		glog.V(4).Info(errors.Wrap(err, "error running command"))
		glog.V(4).Infof("Output is: %v", output)
		return false
	}
	return true
}

// RunLogout logs out the current user from cluster
func (c *Client) RunLogout(stdout io.Writer) error {
	output, err := c.userClient.Users().Get("~", metav1.GetOptions{})
	if err != nil {
		glog.V(1).Infof("%v : unable to get userinfo", err)
	}

	// read the current config form ~/.kube/config
	conf, err := c.KubeConfig.ClientConfig()
	if err != nil {
		glog.V(1).Infof("%v : unable to get client config", err)
	}
	// initialising oauthv1client
	client, err := oauthv1client.NewForConfig(conf)
	if err != nil {
		glog.V(1).Infof("%v : unable to create a new OauthV1Client", err)
	}

	// deleting token form the server
	if err := client.OAuthAccessTokens().Delete(conf.BearerToken, &metav1.DeleteOptions{}); err != nil {
		glog.V(1).Infof("%v", err)
	}

	rawConfig, err := c.KubeConfig.RawConfig()
	if err != nil {
		glog.V(1).Infof("%v : unable to switch to  project", err)
	}

	// deleting token for the current server from local config
	for key, value := range rawConfig.AuthInfos {
		if key == rawConfig.Contexts[rawConfig.CurrentContext].AuthInfo {
			value.Token = ""
		}
	}
	err = clientcmd.ModifyConfig(clientcmd.NewDefaultClientConfigLoadingRules(), rawConfig, true)
	if err != nil {
		glog.V(1).Infof("%v : unable to write config to config file", err)
	}

	_, err = io.WriteString(stdout, fmt.Sprintf("Logged \"%v\" out on \"%v\"\n", output.Name, conf.Host))
	return err
}

// isServerUp returns true if server is up and running
func isServerUp(server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		glog.V(4).Info(errors.Wrap(err, "unable to parse url"))
		return false
	}

	// initialising the default timeout, this will be used
	// when the value is not readable from config
	ocRequestTimeout := config.DefaultTimeout * time.Second
	// checking the value of timeout in config
	// before proceeding with default timeout
	cfg, configReadErr := config.New()
	if configReadErr != nil {
		glog.V(4).Info(errors.Wrap(configReadErr, "unable to read config file"))
	} else {
		ocRequestTimeout = time.Duration(cfg.GetTimeout()) * time.Second
	}
	glog.V(4).Infof("Trying to connect to server %v", u.Host)
	_, connectionError := net.DialTimeout("tcp", u.Host, time.Duration(ocRequestTimeout))
	if connectionError != nil {
		glog.V(4).Info(errors.Wrap(connectionError, "unable to connect to server"))
		return false
	}
	glog.V(4).Infof("Server %v is up", server)
	return true
}

func (c *Client) GetCurrentProjectName() string {
	return c.Namespace
}

// GetProjectNames return list of existing projects that user has access to.
func (c *Client) GetProjectNames() ([]string, error) {
	projects, err := c.projectClient.Projects().List(metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list projects")
	}

	var projectNames []string
	for _, p := range projects.Items {
		projectNames = append(projectNames, p.Name)
	}
	return projectNames, nil
}

// CreateNewProject creates project with given projectName
func (c *Client) CreateNewProject(projectName string) error {
	projectRequest := &projectv1.ProjectRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: projectName,
		},
	}
	_, err := c.projectClient.ProjectRequests().Create(projectRequest)
	if err != nil {
		return errors.Wrapf(err, "unable to create new project %s", projectName)
	}
	return nil
}

// SetCurrentProject sets the given projectName to current project
func (c *Client) SetCurrentProject(projectName string) error {
	rawConfig, err := c.KubeConfig.RawConfig()
	if err != nil {
		return errors.Wrapf(err, "unable to switch to %s project", projectName)
	}

	rawConfig.Contexts[rawConfig.CurrentContext].Namespace = projectName

	err = clientcmd.ModifyConfig(clientcmd.NewDefaultClientConfigLoadingRules(), rawConfig, true)
	if err != nil {
		return errors.Wrapf(err, "unable to switch to %s project", projectName)
	}
	return nil
}

// addLabelsToArgs adds labels from map to args as a new argument in format that oc requires
// --labels label1=value1,label2=value2
func addLabelsToArgs(labels map[string]string, args []string) []string {
	if labels != nil {
		var labelsString []string
		for key, value := range labels {
			labelsString = append(labelsString, fmt.Sprintf("%s=%s", key, value))
		}
		args = append(args, "--labels")
		args = append(args, strings.Join(labelsString, ","))
	}

	return args
}

// getExposedPortsFromISI parse ImageStreamImage definition and return all exposed ports in form of ContainerPorts structs
func getExposedPortsFromISI(image *imagev1.ImageStreamImage) ([]corev1.ContainerPort, error) {
	// file DockerImageMetadata
	imageWithMetadata(&image.Image)

	var ports []corev1.ContainerPort

	for exposedPort := range image.Image.DockerImageMetadata.Object.(*dockerapiv10.DockerImage).ContainerConfig.ExposedPorts {
		splits := strings.Split(exposedPort, "/")
		if len(splits) != 2 {
			return nil, fmt.Errorf("invalid port %s", exposedPort)
		}

		portNumberI64, err := strconv.ParseInt(splits[0], 10, 32)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid port number %s", splits[0])
		}
		portNumber := int32(portNumberI64)

		var portProto corev1.Protocol
		switch strings.ToUpper(splits[1]) {
		case "TCP":
			portProto = corev1.ProtocolTCP
		case "UDP":
			portProto = corev1.ProtocolUDP
		default:
			return nil, fmt.Errorf("invalid port protocol %s", splits[1])
		}

		port := corev1.ContainerPort{
			Name:          fmt.Sprintf("%d-%s", portNumber, strings.ToLower(string(portProto))),
			ContainerPort: portNumber,
			Protocol:      portProto,
		}

		ports = append(ports, port)
	}

	return ports, nil
}

// GetImageStreams returns the Image Stream objects in the given namespace
func (c *Client) GetImageStreams(namespace string) ([]imagev1.ImageStream, error) {
	imageStreamList, err := c.imageClient.ImageStreams(namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list imagestreams")
	}
	return imageStreamList.Items, nil
}

// GetImageStreamsNames returns the names of the image streams in a given
// namespace
func (c *Client) GetImageStreamsNames(namespace string) ([]string, error) {
	imageStreams, err := c.GetImageStreams(namespace)
	if err != nil {
		return nil, errors.Wrap(err, "unable to get image streams")
	}

	var names []string
	for _, imageStream := range imageStreams {
		names = append(names, imageStream.Name)
	}
	return names, nil
}

// isTagInImageStream takes a imagestream and a tag and checks if the tag is present in the imagestream's status attribute
func isTagInImageStream(is imagev1.ImageStream, imageTag string) bool {
	// Loop through the tags in the imagestream's status attribute
	for _, tag := range is.Status.Tags {
		// look for a matching tag
		if tag.Tag == imageTag {
			// Return true if found
			return true
		}
	}
	// Return false if not found.
	return false
}

// GetImageNS returns the imagestream using image details like imageNS, imageName and imageTag
// imageNS can be empty in which case, this function searches currentNamespace on priority. If
// imagestream of required tag not found in current namespace, then searches openshift namespace.
// If not found, error out. If imageNS is not empty string, then, the requested imageNS only is searched
// for requested imagestream
func (c *Client) GetImageStream(imageNS string, imageName string, imageTag string) (*imagev1.ImageStream, error) {
	var err error
	var imageStream *imagev1.ImageStream
	currentProjectName := c.GetCurrentProjectName()
	/*
		If User has not chosen image NS then,
			1. Use image from current NS if available
			2. If not 1, use default openshift NS
			3. If not 2, return errors from both 1 and 2
		else
			Use user chosen namespace
			If image doesn't exist in user chosen namespace,
				error out
			else
				Proceed
	*/
	// User has not passed any particular ImageStream
	if imageNS == "" {

		// First try finding imagestream from current namespace
		currentNSImageStream, e := c.imageClient.ImageStreams(currentProjectName).Get(imageName, metav1.GetOptions{})
		if e != nil {
			err = errors.Wrapf(e, "no match found for : %s in namespace %s", imageName, currentProjectName)
		} else {
			if isTagInImageStream(*currentNSImageStream, imageTag) {
				return currentNSImageStream, nil
			}
		}

		// If not in current namespace, try finding imagestream from openshift namespace
		openshiftNSImageStream, e := c.imageClient.ImageStreams(OpenShiftNameSpace).Get(imageName, metav1.GetOptions{})
		if e != nil {
			// The image is not available in current Namespace.
			err = errors.Wrapf(e, "%s\n.no match found for : %s in namespace %s", err.Error(), imageName, OpenShiftNameSpace)
		} else {
			if isTagInImageStream(*openshiftNSImageStream, imageTag) {
				return openshiftNSImageStream, nil
			}
		}
		if e != nil && err != nil {
			// Imagestream not found in openshift and current namespaces
			return nil, err
		}

		// Required tag not in openshift and current namespaces
		return nil, fmt.Errorf("image stream %s with tag %s not found in openshift and %s namespaces", imageName, imageTag, currentProjectName)

	} else {

		// Fetch imagestream from requested namespace
		imageStream, err = c.imageClient.ImageStreams(imageNS).Get(imageName, metav1.GetOptions{})
		if err != nil {
			return nil, errors.Wrapf(
				err, "no match found for %s in namespace %s", imageName, imageNS,
			)
		}
		if !isTagInImageStream(*imageStream, imageTag) {
			return nil, fmt.Errorf("image stream %s with tag %s not found in %s namespaces", imageName, imageTag, currentProjectName)
		}
	}

	return imageStream, nil
}

// GetSecret returns the Secret object in the given namespace
func (c *Client) GetSecret(name, namespace string) (*corev1.Secret, error) {
	secret, err := c.kubeClient.CoreV1().Secrets(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get the secret %s", secret)
	}
	return secret, nil
}

// GetImageStreamImage returns image and error if any, corresponding to the passed imagestream and image tag
func (c *Client) GetImageStreamImage(imageStream *imagev1.ImageStream, imageTag string) (*imagev1.ImageStreamImage, error) {
	imageNS := imageStream.ObjectMeta.Namespace
	imageName := imageStream.ObjectMeta.Name

	tagFound := false

	for _, tag := range imageStream.Status.Tags {
		// look for matching tag
		if tag.Tag == imageTag {
			tagFound = true
			glog.V(4).Infof("Found exact image tag match for %s:%s", imageName, imageTag)

			if len(tag.Items) > 0 {
				tagDigest := tag.Items[0].Image
				imageStreamImageName := fmt.Sprintf("%s@%s", imageName, tagDigest)

				// look for imageStreamImage for given tag (reference by digest)
				imageStreamImage, err := c.imageClient.ImageStreamImages(imageNS).Get(imageStreamImageName, metav1.GetOptions{})
				if err != nil {
					return nil, errors.Wrapf(err, "unable to find ImageStreamImage with  %s digest", imageStreamImageName)
				}
				return imageStreamImage, nil
			} else {
				return nil, fmt.Errorf("unable to find tag %s for image %s", imageTag, imageName)
			}
		}
	}

	if !tagFound {
		return nil, fmt.Errorf("unable to find tag %s for image %s", imageTag, imageName)
	}

	// return error since its an unhandled case if code reaches here
	return nil, fmt.Errorf("unable to fetch image with tag %s corresponding to imagestream %+v", imageTag, imageStream)
}

// GetExposedPorts returns list of ContainerPorts that are exposed by given image
func (c *Client) GetExposedPorts(imageStreamImage *imagev1.ImageStreamImage) ([]corev1.ContainerPort, error) {
	var containerPorts []corev1.ContainerPort

	// get ports that are exported by image
	containerPorts, err := getExposedPortsFromISI(imageStreamImage)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get exported ports from image %+v", imageStreamImage)
	}

	return containerPorts, nil
}

func getAppRootVolumeName(dcName string) string {
	return fmt.Sprintf("%s-s2idata", dcName)
}

// NewAppS2I is only used with "Git" as we need Build
// gitURL is the url of the git repo
// inputPorts is the array containing the string port values
// envVars is the array containing the string env var values
func (c *Client) NewAppS2I(params CreateArgs, commonObjectMeta metav1.ObjectMeta) error {
	glog.V(4).Infof("Using BuilderImage: %s", params.ImageName)
	imageNS, imageName, imageTag, _, err := ParseImageName(params.ImageName)
	if err != nil {
		return errors.Wrap(err, "unable to parse image name")
	}
	imageStream, err := c.GetImageStream(imageNS, imageName, imageTag)
	if err != nil {
		return errors.Wrap(err, "unable to retrieve ImageStream for NewAppS2I")
	}
	/*
	 Set imageNS to the commonObjectMeta.Namespace of above fetched imagestream because, the commonObjectMeta.Namespace passed here can potentially be emptystring
	 in which case, GetImageStream function resolves to correct commonObjectMeta.Namespace in accordance with priorities in GetImageStream
	*/

	imageNS = imageStream.ObjectMeta.Namespace
	glog.V(4).Infof("Using imageNS: %s", imageNS)

	imageStreamImage, err := c.GetImageStreamImage(imageStream, imageTag)
	if err != nil {
		return errors.Wrapf(err, "unable to create s2i app for %s", commonObjectMeta.Name)
	}

	var containerPorts []corev1.ContainerPort
	if len(params.Ports) == 0 {
		containerPorts, err = c.GetExposedPorts(imageStreamImage)
		if err != nil {
			return errors.Wrapf(err, "unable to get exposed ports for %s:%s", imageName, imageTag)
		}
	} else {
		if err != nil {
			return errors.Wrapf(err, "unable to create s2i app for %s", commonObjectMeta.Name)
		}
		imageNS = imageStream.ObjectMeta.Namespace
		containerPorts, err = getContainerPortsFromStrings(params.Ports)
		if err != nil {
			return errors.Wrapf(err, "unable to get container ports from %v", params.Ports)
		}
	}

	inputEnvVars, err := getInputEnvVarsFromStrings(params.EnvVars)
	if err != nil {
		return errors.Wrapf(err, "error adding environment variables to the container")
	}

	// generate and create ImageStream
	is := imagev1.ImageStream{
		ObjectMeta: commonObjectMeta,
	}
	_, err = c.imageClient.ImageStreams(c.Namespace).Create(&is)
	if err != nil {
		return errors.Wrapf(err, "unable to create ImageStream for %s", commonObjectMeta.Name)
	}

	// if gitURL is not set, error out
	if params.SourcePath == "" {
		return errors.New("unable to create buildSource with empty gitURL")
	}

	// Deploy BuildConfig to build the container with Git
	buildConfig, err := c.CreateBuildConfig(commonObjectMeta, params.ImageName, params.SourcePath, inputEnvVars)
	if err != nil {
		return errors.Wrapf(err, "unable to deploy BuildConfig for %s", commonObjectMeta.Name)
	}

	// Generate and create the DeploymentConfig
	dc := generateGitDeploymentConfig(commonObjectMeta, buildConfig.Spec.Output.To.Name, containerPorts, inputEnvVars, getResourceRequirementsFromRawData(params.Resources))
	_, err = c.appsClient.DeploymentConfigs(c.Namespace).Create(&dc)
	if err != nil {
		return errors.Wrapf(err, "unable to create DeploymentConfig for %s", commonObjectMeta.Name)
	}

	// Create a service
	svc, err := c.CreateService(commonObjectMeta, dc.Spec.Template.Spec.Containers[0].Ports)
	if err != nil {
		return errors.Wrapf(err, "unable to create Service for %s", commonObjectMeta.Name)
	}

	// Create secret(s)
	err = c.createSecrets(params.Name, commonObjectMeta, svc)

	return err
}

// Create a secret for each port, containing the host and port of the component
// This is done so other components can later inject the secret into the environment
// and have the "coordinates" to communicate with this component
func (c *Client) createSecrets(componentName string, commonObjectMeta metav1.ObjectMeta, svc *corev1.Service) error {
	originalName := commonObjectMeta.Name
	for _, svcPort := range svc.Spec.Ports {
		portAsString := fmt.Sprintf("%v", svcPort.Port)

		// we need to create multiple secrets, so each one has to contain the port in it's name
		// so we change the name of each secret by adding the port number
		commonObjectMeta.Name = fmt.Sprintf("%v-%v", originalName, portAsString)

		// we also add the port as an annotation to the secret
		// this comes in handy when we need to "query" for the appropriate secret
		// of a component based on the port
		commonObjectMeta.Annotations[ComponentPortAnnotationName] = portAsString

		err := c.CreateSecret(
			commonObjectMeta,
			map[string]string{
				secretKeyName(componentName, "host"): svc.Name,
				secretKeyName(componentName, "port"): portAsString,
			})

		if err != nil {
			return errors.Wrapf(err, "unable to create Secret for %s", commonObjectMeta.Name)
		}
	}

	// restore the original values of the fields we changed
	commonObjectMeta.Name = originalName
	delete(commonObjectMeta.Annotations, ComponentPortAnnotationName)

	return nil
}

func secretKeyName(componentName, baseKeyName string) string {
	return fmt.Sprintf("COMPONENT_%v_%v", strings.Replace(strings.ToUpper(componentName), "-", "_", -1), strings.ToUpper(baseKeyName))
}

// getS2ILabelValue returns the requested S2I label value from the passed set of labels attached to builder image
// and the hard coded possible list(the labels are not uniform across different builder images) of expected labels
func getS2ILabelValue(labels map[string]string, expectedLabelsSet []string) string {
	for _, label := range expectedLabelsSet {
		if retVal, ok := labels[label]; ok {
			return retVal
		}
	}
	return ""
}

// GetS2IPathsFromBuilderImg returns script path protocol, S2I scripts path, S2I source or binary expected path, S2I deployment dir and errors(if any) from the passed builder image
func GetS2IPathsFromBuilderImg(builderImage *imagev1.ImageStreamImage) (S2IPaths, error) {

	// Define structs for internal un-marshalling of imagestreamimage to extract label from it
	type ContainerConfig struct {
		Labels map[string]string `json:"Labels"`
	}
	type DockerImageMetaDataRaw struct {
		ContainerConfig ContainerConfig `json:"ContainerConfig"`
	}

	var dimdr DockerImageMetaDataRaw

	// The label $S2IScriptsURLLabel needs to be extracted from builderImage#Image#DockerImageMetadata#Raw which is byte array
	dimdrByteArr := (*builderImage).Image.DockerImageMetadata.Raw

	// Unmarshal the byte array into the struct for ease of access of required fields
	err := json.Unmarshal(dimdrByteArr, &dimdr)
	if err != nil {
		return S2IPaths{}, errors.Wrap(err, "unable to bootstrap supervisord")
	}

	// If by any chance, labels attribute is nil(although ideally not the case for builder images), return
	if dimdr.ContainerConfig.Labels == nil {
		glog.V(4).Infof("No Labels found in %+v in builder image %+v", dimdr, builderImage)
		return S2IPaths{}, nil
	}

	// Extract the label containing S2I scripts URL
	s2iScriptsURL := dimdr.ContainerConfig.Labels[S2IScriptsURLLabel]
	s2iSrcOrBinPath := dimdr.ContainerConfig.Labels[S2ISrcOrBinLabel]

	if s2iSrcOrBinPath == "" {
		// In cases like nodejs builder image, where there is no concept of binary and sources are directly run, use destination as source
		// s2iSrcOrBinPath = getS2ILabelValue(dimdr.ContainerConfig.Labels, S2IDeploymentsDir)
		s2iSrcOrBinPath = DefaultS2ISrcOrBinPath
	}

	s2iDestinationDir := getS2ILabelValue(dimdr.ContainerConfig.Labels, S2IDeploymentsDir)
	// The URL is a combination of protocol and the path to script details of which can be found @
	// https://github.com/openshift/source-to-image/blob/master/docs/builder_image.md#s2i-scripts
	// Extract them out into protocol and path separately to minimise the task in
	// https://github.com/redhat-developer/odo-supervisord-image/blob/master/assemble-and-restart when custom handling
	// for each of the protocols is added
	s2iScriptsProtocol := ""
	s2iScriptsPath := ""

	switch {
	case strings.HasPrefix(s2iScriptsURL, "image://"):
		s2iScriptsProtocol = "image://"
		s2iScriptsPath = strings.TrimPrefix(s2iScriptsURL, "image://")
	case strings.HasPrefix(s2iScriptsURL, "file://"):
		s2iScriptsProtocol = "file://"
		s2iScriptsPath = strings.TrimPrefix(s2iScriptsURL, "file://")
	case strings.HasPrefix(s2iScriptsURL, "http(s)://"):
		s2iScriptsProtocol = "http(s)://"
		s2iScriptsPath = s2iScriptsURL
	default:
		return S2IPaths{}, fmt.Errorf("Unknown scripts url %s", s2iScriptsURL)
	}
	return S2IPaths{
		ScriptsPathProtocol: s2iScriptsProtocol,
		ScriptsPath:         s2iScriptsPath,
		SrcOrBinPath:        s2iSrcOrBinPath,
		DeploymentDir:       s2iDestinationDir,
	}, nil
}

// uniqueAppendOrOverwriteEnvVars appends/overwrites the passed existing list of env vars with the elements from the to-be appended passed list of envs
func uniqueAppendOrOverwriteEnvVars(existingEnvs []corev1.EnvVar, envVars ...corev1.EnvVar) []corev1.EnvVar {
	mapExistingEnvs := make(map[string]corev1.EnvVar)
	var retVal []corev1.EnvVar

	// Convert slice of existing env vars to map to check for existence
	for _, envVar := range existingEnvs {
		mapExistingEnvs[envVar.Name] = envVar
	}

	// For each new envVar to be appended, Add(if envVar with same name doesn't already exist) / overwrite(if envVar with same name already exists) the map
	for _, newEnvVar := range envVars {
		mapExistingEnvs[newEnvVar.Name] = newEnvVar
	}

	// Convert map to slice
	for _, envVar := range mapExistingEnvs {
		retVal = append(retVal, envVar)
	}

	return retVal
}

// BootstrapSupervisoredS2I uses S2I (Source To Image) to inject Supervisor into the application container.
// Odo uses https://github.com/ochinchina/supervisord which is pre-built in a ready-to-deploy InitContainer.
// The supervisord binary is copied over to the application container using a temporary volume and overrides
// the built-in S2I run function for the supervisord run command instead.
//
// Supervisor keeps the pod running (as PID 1), so you it is possible to trigger assembly script inside running pod,
// and than restart application using Supervisor without need to restart the container/Pod.
//
func (c *Client) BootstrapSupervisoredS2I(params CreateArgs, commonObjectMeta metav1.ObjectMeta) error {
	imageNS, imageName, imageTag, _, err := ParseImageName(params.ImageName)

	if err != nil {
		return errors.Wrap(err, "unable to create new s2i git build ")
	}
	imageStream, err := c.GetImageStream(imageNS, imageName, imageTag)
	if err != nil {
		return errors.Wrap(err, "Failed to bootstrap supervisored")
	}
	/*
	 Set imageNS to the commonObjectMeta.Namespace of above fetched imagestream because, the commonObjectMeta.Namespace passed here can potentially be emptystring
	 in which case, GetImageStream function resolves to correct commonObjectMeta.Namespace in accordance with priorities in GetImageStream
	*/
	imageNS = imageStream.ObjectMeta.Namespace

	imageStreamImage, err := c.GetImageStreamImage(imageStream, imageTag)
	if err != nil {
		return errors.Wrap(err, "unable to bootstrap supervisord")
	}
	var containerPorts []corev1.ContainerPort
	if len(params.Ports) == 0 {
		containerPorts, err = c.GetExposedPorts(imageStreamImage)
		if err != nil {
			return errors.Wrapf(err, "unable to get exposed ports for %s:%s", imageName, imageTag)
		}
	} else {
		if err != nil {
			return errors.Wrapf(err, "unable to bootstrap s2i supervisored for %s", commonObjectMeta.Name)
		}
		containerPorts, err = getContainerPortsFromStrings(params.Ports)
		if err != nil {
			return errors.Wrapf(err, "unable to get container ports from %v", params.Ports)
		}
	}

	inputEnvs, err := getInputEnvVarsFromStrings(params.EnvVars)
	if err != nil {
		return errors.Wrapf(err, "error adding environment variables to the container")
	}

	// generate and create ImageStream
	is := imagev1.ImageStream{
		ObjectMeta: commonObjectMeta,
	}
	_, err = c.imageClient.ImageStreams(c.Namespace).Create(&is)
	if err != nil {
		return errors.Wrapf(err, "unable to create ImageStream for %s", commonObjectMeta.Name)
	}

	commonImageMeta := CommonImageMeta{
		Name:      imageName,
		Tag:       imageTag,
		Namespace: imageNS,
		Ports:     containerPorts,
	}

	// Extract s2i scripts path and path type from imagestream image
	//s2iScriptsProtocol, s2iScriptsURL, s2iSrcOrBinPath, s2iDestinationDir
	s2iPaths, err := GetS2IPathsFromBuilderImg(imageStreamImage)
	if err != nil {
		return errors.Wrap(err, "unable to bootstrap supervisord")
	}

	// Append s2i related parameters extracted above to env
	inputEnvs = uniqueAppendOrOverwriteEnvVars(
		inputEnvs,
		corev1.EnvVar{
			Name:  EnvS2IScriptsURL,
			Value: s2iPaths.ScriptsPath,
		},
		corev1.EnvVar{
			Name:  EnvS2IScriptsProtocol,
			Value: s2iPaths.ScriptsPathProtocol,
		},
		corev1.EnvVar{
			Name:  EnvS2ISrcOrBinPath,
			Value: s2iPaths.SrcOrBinPath,
		},
		corev1.EnvVar{
			Name:  EnvS2IDeploymentDir,
			Value: s2iPaths.DeploymentDir,
		},
	)

	// Generate the DeploymentConfig that will be used.
	dc := generateSupervisordDeploymentConfig(commonObjectMeta, params.ImageName, commonImageMeta, inputEnvs, getResourceRequirementsFromRawData(params.Resources))

	// Add the appropriate bootstrap volumes for SupervisorD
	addBootstrapVolumeCopyInitContainer(&dc, commonObjectMeta.Name)
	addBootstrapSupervisordInitContainer(&dc, commonObjectMeta.Name)
	addBootstrapVolume(&dc, commonObjectMeta.Name)
	addBootstrapVolumeMount(&dc, commonObjectMeta.Name)

	if len(inputEnvs) != 0 {
		err = updateEnvVar(&dc, inputEnvs)
		if err != nil {
			return errors.Wrapf(err, "unable to add env vars to the container")
		}
	}

	_, err = c.appsClient.DeploymentConfigs(c.Namespace).Create(&dc)
	if err != nil {
		return errors.Wrapf(err, "unable to create DeploymentConfig for %s", commonObjectMeta.Name)
	}

	svc, err := c.CreateService(commonObjectMeta, dc.Spec.Template.Spec.Containers[0].Ports)
	if err != nil {
		return errors.Wrapf(err, "unable to create Service for %s", commonObjectMeta.Name)
	}

	err = c.createSecrets(params.Name, commonObjectMeta, svc)
	if err != nil {
		return err
	}

	// Setup PVC.
	_, err = c.CreatePVC(getAppRootVolumeName(commonObjectMeta.Name), "1Gi", commonObjectMeta.Labels)
	if err != nil {
		return errors.Wrapf(err, "unable to create PVC for %s", commonObjectMeta.Name)
	}

	return nil
}

// CreateService generates and creates the service
// commonObjectMeta is the ObjectMeta for the service
// dc is the deploymentConfig to get the container ports
func (c *Client) CreateService(commonObjectMeta metav1.ObjectMeta, containerPorts []corev1.ContainerPort) (*corev1.Service, error) {
	// generate and create Service
	var svcPorts []corev1.ServicePort
	for _, containerPort := range containerPorts {
		svcPort := corev1.ServicePort{

			Name:       containerPort.Name,
			Port:       containerPort.ContainerPort,
			Protocol:   containerPort.Protocol,
			TargetPort: intstr.FromInt(int(containerPort.ContainerPort)),
		}
		svcPorts = append(svcPorts, svcPort)
	}
	svc := corev1.Service{
		ObjectMeta: commonObjectMeta,
		Spec: corev1.ServiceSpec{
			Ports: svcPorts,
			Selector: map[string]string{
				"deploymentconfig": commonObjectMeta.Name,
			},
		},
	}
	createdSvc, err := c.kubeClient.CoreV1().Services(c.Namespace).Create(&svc)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to create Service for %s", commonObjectMeta.Name)
	}
	return createdSvc, err
}

// CreateSecret generates and creates the secret
// commonObjectMeta is the ObjectMeta for the service
func (c *Client) CreateSecret(objectMeta metav1.ObjectMeta, data map[string]string) error {

	secret := corev1.Secret{
		ObjectMeta: objectMeta,
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
	_, err := c.kubeClient.CoreV1().Secrets(c.Namespace).Create(&secret)
	if err != nil {
		return errors.Wrapf(err, "unable to create secret for %s", objectMeta.Name)
	}
	return nil
}

// updateEnvVar updates the environmental variables to the container in the DC
// dc is the deployment config to be updated
// envVars is the array containing the corev1.EnvVar values
func updateEnvVar(dc *appsv1.DeploymentConfig, envVars []corev1.EnvVar) error {
	numContainers := len(dc.Spec.Template.Spec.Containers)
	if numContainers != 1 {
		return fmt.Errorf("expected exactly one container in Deployment Config %v, got %v", dc.Name, numContainers)
	}

	dc.Spec.Template.Spec.Containers[0].Env = envVars
	return nil
}

// UpdateBuildConfig updates the BuildConfig file
// buildConfigName is the name of the BuildConfig file to be updated
// projectName is the name of the project
// gitURL equals to the git URL of the source and is equals to "" if the source is of type dir or binary
// annotations contains the annotations for the BuildConfig file
func (c *Client) UpdateBuildConfig(buildConfigName string, gitURL string, annotations map[string]string) error {
	if gitURL == "" {
		return errors.New("gitURL for UpdateBuildConfig must not be blank")
	}

	// generate BuildConfig
	buildSource := buildv1.BuildSource{}

	buildSource = buildv1.BuildSource{
		Git: &buildv1.GitBuildSource{
			URI: gitURL,
		},
		Type: buildv1.BuildSourceGit,
	}

	buildConfig, err := c.GetBuildConfigFromName(buildConfigName)
	if err != nil {
		return errors.Wrap(err, "unable to get the BuildConfig file")
	}
	buildConfig.Spec.Source = buildSource
	buildConfig.Annotations = annotations
	_, err = c.buildClient.BuildConfigs(c.Namespace).Update(buildConfig)
	if err != nil {
		return errors.Wrap(err, "unable to update the component")
	}
	return nil
}

// Define a function that is meant to update a DC in place
type dcStructUpdater func(dc *appsv1.DeploymentConfig) error

// PatchCurrentDC "patches" the current DeploymentConfig with a new one
// however... we make sure that configurations such as:
// - volumes
// - environment variables
// are correctly copied over / consistent without an issue.
// if prePatchDCHandler is specified (meaning not nil), then it's applied
// as the last action before the actual call to the Kubernetes API thus giving us the chance
// to perform arbitrary updates to a DC before it's finalized for patching
func (c *Client) PatchCurrentDC(name string, dc appsv1.DeploymentConfig, prePatchDCHandler dcStructUpdater) error {

	// Retrieve the current DC
	currentDC, err := c.GetDeploymentConfigFromName(name)
	if err != nil {
		return errors.Wrapf(err, "unable to get DeploymentConfig %s", name)
	}

	// Find the container (don't want to use .Spec.Containers[0] in case the user has modified the DC...)
	// in order to retrieve what the volumes are
	foundCurrentDCContainer, err := findContainer(currentDC.Spec.Template.Spec.Containers, name)
	if err != nil {
		return errors.Wrapf(err, "Unable to find current DeploymentConfig container %s", name)
	}

	copyVolumesAndVolumeMounts(dc, currentDC, foundCurrentDCContainer)

	if prePatchDCHandler != nil {
		err := prePatchDCHandler(&dc)
		if err != nil {
			return errors.Wrapf(err, "Unable to correctly update dc %s using the specified prePatch handler", name)
		}
	}

	// Replace the current spec with the new one
	currentDC.Spec = dc.Spec

	// Replace the old annotations with the new ones too
	// the reason we do this is because Kubernetes handles metadata such as resourceVersion
	// that should not be overridden.
	currentDC.ObjectMeta.Annotations = dc.ObjectMeta.Annotations
	currentDC.ObjectMeta.Labels = dc.ObjectMeta.Labels

	// Update the current one that's deployed with the new Spec.
	// despite the "patch" function name, we use update since `.Patch` requires
	// use to define each and every object we must change. Updating makes it easier.
	_, err = c.appsClient.DeploymentConfigs(c.Namespace).Update(currentDC)
	if err != nil {
		return errors.Wrapf(err, "unable to update DeploymentConfig %s", name)
	}

	// Watch / wait for deploymentconfig to update annotations
	// importing "component" results in an import loop, so we do *not* use the constants here.
	_, err = c.WaitAndGetDC(name, "app.kubernetes.io/component-source-type", dc.ObjectMeta.Annotations["app.kubernetes.io/component-source-type"], ocUpdateTimeout)
	if err != nil {
		return errors.Wrapf(err, "unable to wait for DeploymentConfig %s to update", name)
	}

	return nil
}

// copies volumes and volume mounts from currentDC to dc, excluding the supervisord related ones
func copyVolumesAndVolumeMounts(dc appsv1.DeploymentConfig, currentDC *appsv1.DeploymentConfig, matchingContainer corev1.Container) {
	// Append the existing VolumeMounts to the new DC. We use "range" and find the correct container rather than
	// using .spec.Containers[0] *in case* the template ever changes and a new container has been added.
	for index, container := range dc.Spec.Template.Spec.Containers {
		// Find the container
		if container.Name == matchingContainer.Name {
			// Loop through all the volumes
			for _, volume := range matchingContainer.VolumeMounts {
				// If it's the supervisord volume, ignore it.
				if volume.Name == supervisordVolumeName {
					continue
				} else {
					dc.Spec.Template.Spec.Containers[index].VolumeMounts = append(dc.Spec.Template.Spec.Containers[index].VolumeMounts, volume)
				}

				// Break out since we've succeeded in updating the container we were looking for
				break
			}
		}
	}
	// Now the same with Volumes, again, ignoring the supervisord volume.
	for _, volume := range currentDC.Spec.Template.Spec.Volumes {
		if volume.Name == supervisordVolumeName {
			continue
		} else {
			dc.Spec.Template.Spec.Volumes = append(dc.Spec.Template.Spec.Volumes, volume)
			break
		}
	}
}

// UpdateDCToGit replaces / updates the current DeplomentConfig with the appropriate
// generated image from BuildConfig as well as the correct DeploymentConfig triggers for Git.
func (c *Client) UpdateDCToGit(commonObjectMeta metav1.ObjectMeta, imageName string) error {

	// Fail if blank
	if imageName == "" {
		return errors.New("UpdateDCToGit imageName cannot be blank")
	}

	// Retrieve the current DC in order to obtain what the current inputPorts are..
	currentDC, err := c.GetDeploymentConfigFromName(commonObjectMeta.Name)
	if err != nil {
		return errors.Wrapf(err, "unable to get DeploymentConfig %s", commonObjectMeta.Name)
	}

	// Find the container (don't want to use .Spec.Containers[0] in case the user has modified the DC...)
	foundCurrentDCContainer, err := findContainer(currentDC.Spec.Template.Spec.Containers, commonObjectMeta.Name)
	if err != nil {
		return errors.Wrapf(err, "Unable to find container %s", commonObjectMeta.Name)
	}

	// Generate the new DeploymentConfig
	resourceLimits := fetchContainerResourceLimits(foundCurrentDCContainer)
	dc := generateGitDeploymentConfig(commonObjectMeta, imageName, foundCurrentDCContainer.Ports, foundCurrentDCContainer.Env, &resourceLimits)

	// Patch the current DC
	err = c.PatchCurrentDC(commonObjectMeta.Name, dc, removeTracesOfSupervisordFromDC)
	if err != nil {
		return errors.Wrapf(err, "unable to update the current DeploymentConfig %s", commonObjectMeta.Name)
	}

	// Cleanup after the supervisor
	err = c.DeletePVC(getAppRootVolumeName(commonObjectMeta.Name))
	if err != nil {
		return errors.Wrapf(err, "unable to delete S2I data PVC from %s", commonObjectMeta.Name)
	}

	return nil
}

// UpdateDCToSupervisor updates the current DeploymentConfig to a SupervisorD configuration.
func (c *Client) UpdateDCToSupervisor(commonObjectMeta metav1.ObjectMeta, componentImageType string) error {

	// Parse the image
	imageNS, imageName, imageTag, _, err := ParseImageName(componentImageType)
	if err != nil {
		return errors.Wrap(err, "unable to parse image name for DeploymentConfig update")
	}

	// Retrieve the namespace of the corresponding component image
	imageStream, err := c.GetImageStream(imageNS, imageName, imageTag)
	if err != nil {
		return errors.Wrap(err, "unable to get image stream for CreateBuildConfig")
	}
	imageNS = imageStream.ObjectMeta.Namespace

	imageStreamImage, err := c.GetImageStreamImage(imageStream, imageTag)
	if err != nil {
		return errors.Wrap(err, "unable to bootstrap supervisord")
	}

	// Retrieve the current DC in order to obtain what the current inputPorts are..
	currentDC, err := c.GetDeploymentConfigFromName(commonObjectMeta.Name)
	if err != nil {
		return errors.Wrapf(err, "unable to get DeploymentConfig %s", commonObjectMeta.Name)
	}

	// Find the container (don't want to use .Spec.Containers[0] in case the user has modified the DC...)
	foundCurrentDCContainer, err := findContainer(currentDC.Spec.Template.Spec.Containers, commonObjectMeta.Name)
	if err != nil {
		return errors.Wrapf(err, "Unable to find container %s", commonObjectMeta.Name)
	}

	// Gather the common image data into one struct
	commonImageMeta := CommonImageMeta{
		Name:      imageName,
		Tag:       imageTag,
		Namespace: imageNS,
		Ports:     foundCurrentDCContainer.Ports,
	}

	s2iPaths, err := GetS2IPathsFromBuilderImg(imageStreamImage)
	if err != nil {
		return errors.Wrap(err, "unable to bootstrap supervisord")
	}

	// Append s2i related parameters extracted above to env
	inputEnvs := uniqueAppendOrOverwriteEnvVars(
		foundCurrentDCContainer.Env,
		corev1.EnvVar{
			Name:  EnvS2IScriptsURL,
			Value: s2iPaths.ScriptsPath,
		},
		corev1.EnvVar{
			Name:  EnvS2IScriptsProtocol,
			Value: s2iPaths.ScriptsPathProtocol,
		},
		corev1.EnvVar{
			Name:  EnvS2ISrcOrBinPath,
			Value: s2iPaths.SrcOrBinPath,
		},
		corev1.EnvVar{
			Name:  EnvS2IDeploymentDir,
			Value: s2iPaths.DeploymentDir,
		},
	)

	// Generate the SupervisorD Config
	resourceLimits := fetchContainerResourceLimits(foundCurrentDCContainer)
	dc := generateSupervisordDeploymentConfig(commonObjectMeta, componentImageType, commonImageMeta, inputEnvs, &resourceLimits)

	// Add the appropriate bootstrap volumes for SupervisorD
	addBootstrapVolumeCopyInitContainer(&dc, commonObjectMeta.Name)
	addBootstrapSupervisordInitContainer(&dc, commonObjectMeta.Name)
	addBootstrapVolume(&dc, commonObjectMeta.Name)
	addBootstrapVolumeMount(&dc, commonObjectMeta.Name)

	// Patch the current DC with the new one
	err = c.PatchCurrentDC(commonObjectMeta.Name, dc, nil)
	if err != nil {
		return errors.Wrapf(err, "unable to update the current DeploymentConfig %s", commonObjectMeta.Name)
	}

	// Setup PVC
	_, err = c.CreatePVC(getAppRootVolumeName(commonObjectMeta.Name), "1Gi", commonObjectMeta.Labels)
	if err != nil {
		return errors.Wrapf(err, "unable to create PVC for %s", commonObjectMeta.Name)
	}

	return nil
}

// UpdateDCAnnotations updates the DeploymentConfig file
// dcName is the name of the DeploymentConfig file to be updated
// annotations contains the annotations for the DeploymentConfig file
func (c *Client) UpdateDCAnnotations(dcName string, annotations map[string]string) error {
	dc, err := c.GetDeploymentConfigFromName(dcName)
	if err != nil {
		return errors.Wrapf(err, "unable to get DeploymentConfig %s", dcName)
	}

	dc.Annotations = annotations
	_, err = c.appsClient.DeploymentConfigs(c.Namespace).Update(dc)
	if err != nil {
		return errors.Wrapf(err, "unable to uDeploymentConfig config %s", dcName)
	}
	return nil
}

// SetupForSupervisor adds the supervisor to the deployment config
// dcName is the name of the deployment config to be updated
// projectName is the name of the project
// annotations are the updated annotations for the new deployment config
// labels are the labels of the PVC created while setting up the supervisor
func (c *Client) SetupForSupervisor(dcName string, annotations map[string]string, labels map[string]string) error {
	dc, err := c.GetDeploymentConfigFromName(dcName)
	if err != nil {
		return errors.Wrapf(err, "unable to get DeploymentConfig %s", dcName)
	}

	dc.Annotations = annotations

	addBootstrapVolumeCopyInitContainer(dc, dcName)

	addBootstrapVolume(dc, dcName)

	addBootstrapVolumeMount(dc, dcName)

	_, err = c.appsClient.DeploymentConfigs(c.Namespace).Update(dc)
	if err != nil {
		return errors.Wrapf(err, "unable to uDeploymentConfig config %s", dcName)
	}
	_, err = c.CreatePVC(getAppRootVolumeName(dcName), "1Gi", labels)
	if err != nil {
		return errors.Wrapf(err, "unable to create PVC for %s", dcName)
	}
	return nil
}

// removeTracesOfSupervisordFromDC takes a DeploymentConfig and removes any traces of the supervisord from it
// so it removes things like supervisord volumes, volumes mounts and init containers
func removeTracesOfSupervisordFromDC(dc *appsv1.DeploymentConfig) error {
	dcName := dc.Name

	found := removeVolumeFromDC(getAppRootVolumeName(dcName), dc)
	if !found {
		return errors.New("unable to find volume in dc with name: " + dcName)
	}
	found = removeVolumeMountFromDC(getAppRootVolumeName(dcName), dc)
	if !found {
		return errors.New("unable to find volume mount in dc with name: " + dcName)
	}

	// remove the one bootstrapped init container
	for i, container := range dc.Spec.Template.Spec.InitContainers {
		if container.Name == "copy-files-to-volume" {
			dc.Spec.Template.Spec.InitContainers = append(dc.Spec.Template.Spec.InitContainers[:i], dc.Spec.Template.Spec.InitContainers[i+1:]...)
		}
	}

	return nil
}

// GetLatestBuildName gets the name of the latest build
// buildConfigName is the name of the buildConfig for which we are fetching the build name
// returns the name of the latest build or the error
func (c *Client) GetLatestBuildName(buildConfigName string) (string, error) {
	buildConfig, err := c.buildClient.BuildConfigs(c.Namespace).Get(buildConfigName, metav1.GetOptions{})
	if err != nil {
		return "", errors.Wrap(err, "unable to get the latest build name")
	}
	return fmt.Sprintf("%s-%d", buildConfigName, buildConfig.Status.LastVersion), nil
}

// StartBuild starts new build as it is, returns name of the build stat was started
func (c *Client) StartBuild(name string) (string, error) {
	glog.V(4).Infof("Build %s started.", name)
	buildRequest := buildv1.BuildRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	result, err := c.buildClient.BuildConfigs(c.Namespace).Instantiate(name, &buildRequest)
	if err != nil {
		return "", errors.Wrapf(err, "unable to instantiate BuildConfig for %s", name)
	}
	glog.V(4).Infof("Build %s for BuildConfig %s triggered.", name, result.Name)

	return result.Name, nil
}

// WaitForBuildToFinish block and waits for build to finish. Returns error if build failed or was canceled.
func (c *Client) WaitForBuildToFinish(buildName string) error {
	glog.V(4).Infof("Waiting for %s  build to finish", buildName)

	w, err := c.buildClient.Builds(c.Namespace).Watch(metav1.ListOptions{
		FieldSelector: fields.Set{"metadata.name": buildName}.AsSelector().String(),
	})
	if err != nil {
		return errors.Wrapf(err, "unable to watch build")
	}
	defer w.Stop()
	for {
		val, ok := <-w.ResultChan()
		if !ok {
			break
		}
		if e, ok := val.Object.(*buildv1.Build); ok {
			glog.V(4).Infof("Status of %s build is %s", e.Name, e.Status.Phase)
			switch e.Status.Phase {
			case buildv1.BuildPhaseComplete:
				glog.V(4).Infof("Build %s completed.", e.Name)
				return nil
			case buildv1.BuildPhaseFailed, buildv1.BuildPhaseCancelled, buildv1.BuildPhaseError:
				return errors.Errorf("build %s status %s", e.Name, e.Status.Phase)
			}
		}
	}
	return nil
}

// WaitAndGetDC block and waits until the DeploymentConfig has updated it's annotation
// It will *wait* until "value" is expected within the DeploymentConfig.
func (c *Client) WaitAndGetDC(name string, field string, value string, timeout time.Duration) (*appsv1.DeploymentConfig, error) {
	glog.V(4).Infof("Waiting for DeploymentConfig %s annotation '%s' to update to '%s'", name, field, value)

	w, err := c.appsClient.DeploymentConfigs(c.Namespace).Watch(metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", name),
	})
	defer w.Stop()

	if err != nil {
		return nil, errors.Wrapf(err, "unable to watch dc")
	}

	timeoutChannel := time.After(timeout)
	// Keep trying until we're timed out or got a result or got an error
	for {
		select {

		// Timout after X amount of seconds
		case <-timeoutChannel:
			return nil, errors.New("Timed out waiting for annotation to update")

		// Each loop we check the result
		case val, ok := <-w.ResultChan():

			if !ok {
				break
			}
			if e, ok := val.Object.(*appsv1.DeploymentConfig); ok {

				glog.V(4).Infof("Current annotation: %s=%s", field, e.Annotations[field])

				// If the annotation has been updated, let's exit
				if e.Annotations[field] == value {
					glog.V(4).Infof("DeploymentConfig %s annotation %s has been updated to %s", name, field, e.Annotations[field])
					return e, nil
				}

			}
		}
	}
}

// WaitAndGetPod block and waits until pod matching selector is in in Running state
func (c *Client) WaitAndGetPod(selector string) (*corev1.Pod, error) {
	glog.V(4).Infof("Waiting for %s pod", selector)

	w, err := c.kubeClient.CoreV1().Pods(c.Namespace).Watch(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to watch pod")
	}
	defer w.Stop()

	podChannel := make(chan *corev1.Pod)
	watchErrorChannel := make(chan error)

	go func() {
	loop:
		for {
			val, ok := <-w.ResultChan()
			if !ok {
				watchErrorChannel <- errors.New("watch channel was closed")
				break loop
			}
			if e, ok := val.Object.(*corev1.Pod); ok {
				glog.V(4).Infof("Status of %s pod is %s", e.Name, e.Status.Phase)
				switch e.Status.Phase {
				case corev1.PodRunning:
					glog.V(4).Infof("Pod %s is running.", e.Name)
					podChannel <- e
					break loop
				case corev1.PodFailed, corev1.PodUnknown:
					watchErrorChannel <- errors.Errorf("pod %s status %s", e.Name, e.Status.Phase)
					break loop
				}
			} else {
				watchErrorChannel <- errors.New("unable to convert event object to Pod")
				break loop
			}
		}
		close(podChannel)
		close(watchErrorChannel)
	}()

	select {
	case val := <-podChannel:
		return val, nil
	case err := <-watchErrorChannel:
		return nil, err
	case <-time.After(waitForPodTimeOut):
		return nil, errors.Errorf("waited %s but couldn't find running pod matching selector: '%s'", waitForPodTimeOut, selector)
	}
}

// WaitAndGetSecret blocks and waits until the secret is available
func (c *Client) WaitAndGetSecret(name string, namespace string) (*corev1.Secret, error) {
	glog.V(4).Infof("Waiting for secret %s to become available", name)

	w, err := c.kubeClient.CoreV1().Secrets(namespace).Watch(metav1.ListOptions{
		FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String(),
	})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to watch secret")
	}
	defer w.Stop()
	for {
		val, ok := <-w.ResultChan()
		if !ok {
			break
		}
		if e, ok := val.Object.(*corev1.Secret); ok {
			glog.V(4).Infof("Secret %s now exists", e.Name)
			return e, nil
		}
	}
	return nil, errors.Errorf("unknown error while waiting for secret '%s'", name)
}

// FollowBuildLog stream build log to stdout
func (c *Client) FollowBuildLog(buildName string, stdout io.Writer) error {
	buildLogOptions := buildv1.BuildLogOptions{
		Follow: true,
		NoWait: false,
	}

	rd, err := c.buildClient.RESTClient().Get().
		Namespace(c.Namespace).
		Resource("builds").
		Name(buildName).
		SubResource("log").
		VersionedParams(&buildLogOptions, buildschema.ParameterCodec).
		Stream()

	if err != nil {
		return errors.Wrapf(err, "unable get build log %s", buildName)
	}
	defer rd.Close()

	// Set the colour of the stdout output..
	color.Set(color.FgYellow)
	defer color.Unset()

	if _, err = io.Copy(stdout, rd); err != nil {
		return errors.Wrapf(err, "error streaming logs for %s", buildName)
	}

	return nil
}

// Display DeploymentConfig log to stdout
func (c *Client) DisplayDeploymentConfigLog(deploymentConfigName string, followLog bool, stdout io.Writer) error {

	// Set standard log options
	deploymentLogOptions := appsv1.DeploymentLogOptions{Follow: false, NoWait: true}

	// If the log is being followed, set it to follow / don't wait
	if followLog {
		// TODO: https://github.com/kubernetes/kubernetes/pull/60696
		// Unable to set to 0, until openshift/client-go updates their Kubernetes vendoring to 1.11.0
		// Set to 1 for now.
		tailLines := int64(1)
		deploymentLogOptions = appsv1.DeploymentLogOptions{Follow: true, NoWait: false, Previous: false, TailLines: &tailLines}
	}

	// RESTClient call to OpenShift
	rd, err := c.appsClient.RESTClient().Get().
		Namespace(c.Namespace).
		Name(deploymentConfigName).
		Resource("deploymentconfigs").
		SubResource("log").
		VersionedParams(&deploymentLogOptions, appsschema.ParameterCodec).
		Stream()
	if err != nil {
		return errors.Wrapf(err, "unable get deploymentconfigs log %s", deploymentConfigName)
	}
	if rd == nil {
		return errors.New("unable to retrieve DeploymentConfig from OpenShift, does your component exist?")
	}
	defer rd.Close()

	// Copy to stdout (in yellow)
	color.Set(color.FgYellow)
	defer color.Unset()

	// If we are going to followLog, we'll be copying it to stdout
	// else, we copy it to a buffer
	if followLog {

		if _, err = io.Copy(stdout, rd); err != nil {
			return errors.Wrapf(err, "error followLoging logs for %s", deploymentConfigName)
		}

	} else {

		// Copy to buffer (we aren't going to be followLoging the logs..)
		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, rd)
		if err != nil {
			return errors.Wrapf(err, "unable to copy followLog to buffer")
		}

		// Copy to stdout
		if _, err = io.Copy(stdout, buf); err != nil {
			return errors.Wrapf(err, "error copying logs to stdout")
		}

	}

	return nil
}

// Delete takes labels as a input and based on it, deletes respective resource
func (c *Client) Delete(labels map[string]string) error {
	// convert labels to selector
	selector := util.ConvertLabelsToSelector(labels)
	glog.V(4).Infof("Selectors used for deletion: %s", selector)

	var errorList []string
	// Delete DeploymentConfig
	glog.V(4).Info("Deleting DeploymentConfigs")
	err := c.appsClient.DeploymentConfigs(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to delete deploymentconfig")
	}
	// Delete Route
	glog.V(4).Info("Deleting Routes")
	err = c.routeClient.Routes(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to delete route")
	}
	// Delete BuildConfig
	glog.V(4).Info("Deleting BuildConfigs")
	err = c.buildClient.BuildConfigs(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to delete buildconfig")
	}
	// Delete ImageStream
	glog.V(4).Info("Deleting ImageStreams")
	err = c.imageClient.ImageStreams(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to delete imagestream")
	}
	// Delete Services
	glog.V(4).Info("Deleting Services")
	svcList, err := c.kubeClient.CoreV1().Services(c.Namespace).List(metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to list services")
	}
	for _, svc := range svcList.Items {
		err = c.kubeClient.CoreV1().Services(c.Namespace).Delete(svc.Name, &metav1.DeleteOptions{})
		if err != nil {
			errorList = append(errorList, "unable to delete service")
		}
	}
	// PersistentVolumeClaim
	glog.V(4).Infof("Deleting PersistentVolumeClaims")
	err = c.kubeClient.CoreV1().PersistentVolumeClaims(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to delete volume")
	}
	// Secret
	glog.V(4).Infof("Deleting Secret")
	err = c.kubeClient.CoreV1().Secrets(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		errorList = append(errorList, "unable to delete secret")
	}

	// Error string
	errString := strings.Join(errorList, ",")
	if len(errString) != 0 {
		return errors.New(errString)
	}
	return nil

}

// DeleteServiceInstance takes labels as a input and based on it, deletes respective service instance
func (c *Client) DeleteServiceInstance(labels map[string]string) error {
	glog.V(4).Infof("Deleting Service Instance")

	// convert labels to selector
	selector := util.ConvertLabelsToSelector(labels)
	glog.V(4).Infof("Selectors used for deletion: %s", selector)

	// Listing out serviceInstance because `DeleteCollection` method don't work on serviceInstance
	svcCatList, err := c.GetServiceInstanceList(selector)
	if err != nil {
		return errors.Wrap(err, "unable to list service instance")
	}

	// Iterating over serviceInstance List and deleting one by one
	for _, svc := range svcCatList {
		// we need to delete the ServiceBinding before deleting the ServiceInstance
		err = c.serviceCatalogClient.ServiceBindings(c.Namespace).Delete(svc.Name, &metav1.DeleteOptions{})
		if err != nil {
			return errors.Wrap(err, "unable to delete serviceBinding")
		}
		// now we perform the actual deletion
		err = c.serviceCatalogClient.ServiceInstances(c.Namespace).Delete(svc.Name, &metav1.DeleteOptions{})
		if err != nil {
			return errors.Wrap(err, "unable to delete serviceInstance")
		}
	}

	return nil
}

// DeleteProject deletes given project
func (c *Client) DeleteProject(name string) error {
	err := c.projectClient.Projects().Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		return errors.Wrap(err, "unable to delete project")
	}

	// wait for delete to complete
	w, err := c.projectClient.Projects().Watch(metav1.ListOptions{
		FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String(),
	})
	if err != nil {
		return errors.Wrapf(err, "unable to watch project")
	}

	defer w.Stop()
	for {
		val, ok := <-w.ResultChan()
		// When marked for deletion... val looks like:
		/*
			val: {
				Type:MODIFIED
				Object:&Project{
					ObjectMeta:k8s_io_apimachinery_pkg_apis_meta_v1.ObjectMeta{...},
					Spec:ProjectSpec{...},
					Status:ProjectStatus{
						Phase:Terminating,
					},
				}
			}
		*/
		// Post deletion val will look like:
		/*
			val: {
				Type:DELETED
				Object:&Project{
					ObjectMeta:k8s_io_apimachinery_pkg_apis_meta_v1.ObjectMeta{...},
					Spec:ProjectSpec{...},
					Status:ProjectStatus{
						Phase:,
					},
				}
			}
		*/
		if !ok {
			return fmt.Errorf("received unexpected signal %+v on project watch channel", val)
		}
		// So we depend on val.Type as val.Object.Status.Phase is just empty string and not a mapped value constant
		if prj, ok := val.Object.(*projectv1.Project); ok {
			glog.V(4).Infof("Status of delete of project %s is %s", name, prj.Status.Phase)
			switch prj.Status.Phase {
			//prj.Status.Phase can only be "Terminating" or "Active" or ""
			case "":
				if val.Type == watch.Deleted {
					return nil
				}
				if val.Type == watch.Error {
					return fmt.Errorf("failed watching the deletion of project %s", name)
				}
			}
		}
	}
}

// GetLabelValues get label values of given label from objects in project that are matching selector
// returns slice of unique label values
func (c *Client) GetLabelValues(label string, selector string) ([]string, error) {
	// List DeploymentConfig according to selectors
	dcList, err := c.appsClient.DeploymentConfigs(c.Namespace).List(metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list DeploymentConfigs")
	}
	var values []string
	for _, elem := range dcList.Items {
		for key, val := range elem.Labels {
			if key == label {
				values = append(values, val)
			}
		}
	}

	return values, nil
}

// GetServiceInstanceList returns list service instances
func (c *Client) GetServiceInstanceList(selector string) ([]scv1beta1.ServiceInstance, error) {
	// List ServiceInstance according to given selectors
	svcList, err := c.serviceCatalogClient.ServiceInstances(c.Namespace).List(metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list ServiceInstances")
	}

	return svcList.Items, nil
}

// GetBuildConfigFromName get BuildConfig by its name
func (c *Client) GetBuildConfigFromName(name string) (*buildv1.BuildConfig, error) {
	glog.V(4).Infof("Getting BuildConfig: %s", name)
	bc, err := c.buildClient.BuildConfigs(c.Namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get BuildConfig %s", name)
	}
	return bc, nil
}

// GetClusterServiceClasses queries the service service catalog to get available clusterServiceClasses
func (c *Client) GetClusterServiceClasses() ([]scv1beta1.ClusterServiceClass, error) {
	classList, err := c.serviceCatalogClient.ClusterServiceClasses().List(metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list cluster service classes")
	}
	return classList.Items, nil
}

// GetClusterServiceClass returns the required service class from the service name
// serviceName is the name of the service
// returns the required service class and the error
func (c *Client) GetClusterServiceClass(serviceName string) (*scv1beta1.ClusterServiceClass, error) {
	opts := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.externalName", serviceName).String(),
	}
	searchResults, err := c.serviceCatalogClient.ClusterServiceClasses().List(opts)
	if err != nil {
		return nil, fmt.Errorf("unable to search classes by name (%s)", err)
	}
	if len(searchResults.Items) == 0 {
		return nil, fmt.Errorf("class '%s' not found", serviceName)
	}
	if len(searchResults.Items) > 1 {
		return nil, fmt.Errorf("more than one matching class found for '%s'", serviceName)
	}
	return &searchResults.Items[0], nil
}

// GetClusterPlansFromServiceName returns the plans associated with a service class
// serviceName is the name of the service class whose plans are required
// returns array of ClusterServicePlans or error
func (c *Client) GetClusterPlansFromServiceName(serviceName string) ([]scv1beta1.ClusterServicePlan, error) {
	opts := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.clusterServiceClassRef.name", serviceName).String(),
	}
	searchResults, err := c.serviceCatalogClient.ClusterServicePlans().List(opts)
	if err != nil {
		return nil, fmt.Errorf("unable to search plans for service name '%s', (%s)", serviceName, err)
	}
	return searchResults.Items, nil
}

// CreateServiceInstance creates service instance from service catalog
func (c *Client) CreateServiceInstance(serviceName string, serviceType string, servicePlan string, parameters map[string]string, labels map[string]string) error {
	serviceInstanceParameters, err := serviceInstanceParameters(parameters)
	if err != nil {
		return errors.Wrap(err, "unable to create the service instance parameters")
	}

	_, err = c.serviceCatalogClient.ServiceInstances(c.Namespace).Create(
		&scv1beta1.ServiceInstance{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ServiceInstance",
				APIVersion: "servicecatalog.k8s.io/v1beta1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
				Namespace: c.Namespace,
				Labels:    labels,
			},
			Spec: scv1beta1.ServiceInstanceSpec{
				PlanReference: scv1beta1.PlanReference{
					ClusterServiceClassExternalName: serviceType,
					ClusterServicePlanExternalName:  servicePlan,
				},
				Parameters: serviceInstanceParameters,
			},
		})

	if err != nil {
		return errors.Wrapf(err, "unable to create the service instance %s for the service type %s and plan %s", serviceName, serviceType, servicePlan)
	}

	// Create the secret containing the parameters of the plan selected.
	err = c.CreateServiceBinding(serviceName, c.Namespace)
	if err != nil {
		return errors.Wrapf(err, "unable to create the secret %s for the service instance", serviceName)
	}

	return nil
}

// CreateServiceBinding creates a ServiceBinding (essentially a secret) within the namespace of the
// service instance created using the service's parameters.
func (c *Client) CreateServiceBinding(componentName string, namespace string) error {
	_, err := c.serviceCatalogClient.ServiceBindings(namespace).Create(
		&scv1beta1.ServiceBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      componentName,
				Namespace: namespace,
			},
			Spec: scv1beta1.ServiceBindingSpec{
				//ExternalID: UUID,
				ServiceInstanceRef: scv1beta1.LocalObjectReference{
					Name: componentName,
				},
				SecretName: componentName,
			},
		})

	if err != nil {
		return errors.Wrap(err, "Creation of the secret failed")
	}

	return nil
}

// GetServiceBinding returns the ServiceBinding named serviceName in the namespace namespace
func (c *Client) GetServiceBinding(serviceName string, namespace string) (*scv1beta1.ServiceBinding, error) {
	return c.serviceCatalogClient.ServiceBindings(namespace).Get(serviceName, metav1.GetOptions{})
}

// serviceInstanceParameters converts a map of variable assignments to a byte encoded json document,
// which is what the ServiceCatalog API consumes.
func serviceInstanceParameters(params map[string]string) (*runtime.RawExtension, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return &runtime.RawExtension{Raw: paramsJSON}, nil
}

// LinkSecret links a secret to the DeploymentConfig of a component
func (c *Client) LinkSecret(secretName, componentName, applicationName, namespace string) error {
	dcName, err := util.NamespaceOpenShiftObject(componentName, applicationName)
	if err != nil {
		return err
	}

	dc, err := c.appsClient.DeploymentConfigs(namespace).Get(dcName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "Unable to locate DeploymentConfig for component %s of application %s", componentName, applicationName)
	}

	// Add the Secret as EnvVar to the container
	dc.Spec.Template.Spec.Containers[0].EnvFrom =
		append(
			dc.Spec.Template.Spec.Containers[0].EnvFrom,
			corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				},
			},
		)

	// update the DeploymentConfig with the secret
	_, err = c.appsClient.DeploymentConfigs(namespace).Update(dc)
	if err != nil {
		return errors.Wrapf(err, "DeploymentConfig not updated %s", dc.Name)
	}

	// Create a request that we will pass to the Deployment Config in order to trigger a new deployment
	request := &appsv1.DeploymentRequest{
		Name:   dcName,
		Latest: true,
		Force:  true,
	}

	// Redeploy the DeploymentConfig of the application
	// This is needed for the newly added secret to be injected to the pod
	_, err = c.appsClient.DeploymentConfigs(namespace).Instantiate(request.Name, request)
	if err != nil {
		return errors.Wrapf(err, "Redeployment of the DeploymentConfig failed %s", request.Name)
	}

	return nil
}

// Service struct holds the servicename and it's corresponding list of plans
type Service struct {
	Name     string
	PlanList []string
}

// GetClusterServiceClassExternalNamesAndPlans returns the names of all the cluster service
// classes in the cluster
func (c *Client) GetClusterServiceClassExternalNamesAndPlans() ([]Service, error) {
	var classNames []Service

	classes, err := c.GetClusterServiceClasses()
	if err != nil {
		return nil, errors.Wrap(err, "unable to get cluster service classes")
	}

	planListItems, err := c.GetAllClusterServicePlans()
	if err != nil {
		return nil, errors.Wrap(err, "Unable to get service plans")
	}
	for _, class := range classes {
		var planList []string
		for _, plan := range planListItems {
			if plan.Spec.ClusterServiceClassRef.Name == class.Spec.ExternalID {
				planList = append(planList, plan.Spec.ExternalName)
			}
		}

		classNames = append(classNames, Service{Name: class.Spec.ExternalName, PlanList: planList})
	}
	return classNames, nil
}

// GetAllClusterServicePlans returns list of available plans
func (c *Client) GetAllClusterServicePlans() ([]scv1beta1.ClusterServicePlan, error) {
	planList, err := c.serviceCatalogClient.ClusterServicePlans().List(metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "unable to get cluster service plan")
	}

	return planList.Items, nil
}

// imageStreamExists returns true if the given image stream exists in the given
// namespace
func (c *Client) imageStreamExists(name string, namespace string) bool {
	imageStreams, err := c.GetImageStreamsNames(namespace)
	if err != nil {
		glog.V(4).Infof("unable to get image streams in the namespace: %v", namespace)
		return false
	}

	for _, is := range imageStreams {
		if is == name {
			return true
		}
	}
	return false
}

// clusterServiceClassExists returns true if the given external name of the
// cluster service class exists in the cluster, and false otherwise
func (c *Client) clusterServiceClassExists(name string) bool {
	clusterServiceClasses, err := c.GetClusterServiceClassExternalNamesAndPlans()
	if err != nil {
		glog.V(4).Infof("unable to get cluster service classes' external names")
	}

	for _, class := range clusterServiceClasses {
		if class.Name == name {
			return true
		}
	}

	return false
}

// CreateRoute creates a route object for the given service and with the given labels
// serviceName is the name of the service for the target reference
// portNumber is the target port of the route
func (c *Client) CreateRoute(name string, serviceName string, portNumber intstr.IntOrString, labels map[string]string) (*routev1.Route, error) {
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: serviceName,
			},
			Port: &routev1.RoutePort{
				TargetPort: portNumber,
			},
		},
	}
	r, err := c.routeClient.Routes(c.Namespace).Create(route)
	if err != nil {
		return nil, errors.Wrap(err, "error creating route")
	}
	return r, nil
}

// DeleteRoute deleted the given route
func (c *Client) DeleteRoute(name string) error {
	err := c.routeClient.Routes(c.Namespace).Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		return errors.Wrap(err, "unable to delete route")
	}
	return nil
}

// ListRoutes lists all the routes based on the given label selector
func (c *Client) ListRoutes(labelSelector string) ([]routev1.Route, error) {
	routeList, err := c.routeClient.Routes(c.Namespace).List(metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, errors.Wrap(err, "unable to get route list")
	}

	return routeList.Items, nil
}

// ListRouteNames lists all the names of the routes based on the given label
// selector
func (c *Client) ListRouteNames(labelSelector string) ([]string, error) {
	routes, err := c.ListRoutes(labelSelector)
	if err != nil {
		return nil, errors.Wrap(err, "unable to list routes")
	}

	var routeNames []string
	for _, r := range routes {
		routeNames = append(routeNames, r.Name)
	}

	return routeNames, nil
}

// ListSecrets lists all the secrets based on the given label selector
func (c *Client) ListSecrets(labelSelector string) ([]corev1.Secret, error) {
	listOptions := metav1.ListOptions{}
	if len(labelSelector) > 0 {
		listOptions = metav1.ListOptions{
			LabelSelector: labelSelector,
		}
	}

	secretList, err := c.kubeClient.CoreV1().Secrets(c.Namespace).List(listOptions)
	if err != nil {
		return nil, errors.Wrap(err, "unable to get secret list")
	}

	return secretList.Items, nil
}

// CreatePVC creates a PVC resource in the cluster with the given name, size and
// labels
func (c *Client) CreatePVC(name string, size string, labels map[string]string) (*corev1.PersistentVolumeClaim, error) {
	quantity, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to parse size: %v", size)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: quantity,
				},
			},
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
		},
	}

	createdPvc, err := c.kubeClient.CoreV1().PersistentVolumeClaims(c.Namespace).Create(pvc)
	if err != nil {
		return nil, errors.Wrap(err, "unable to create PVC")
	}
	return createdPvc, nil
}

// DeletePVC deletes the given PVC by name
func (c *Client) DeletePVC(name string) error {
	return c.kubeClient.CoreV1().PersistentVolumeClaims(c.Namespace).Delete(name, nil)
}

// DeleteBuildConfig deletes the given BuildConfig by name using CommonObjectMeta..
func (c *Client) DeleteBuildConfig(commonObjectMeta metav1.ObjectMeta) error {

	// Convert labels to selector
	selector := util.ConvertLabelsToSelector(commonObjectMeta.Labels)
	glog.V(4).Infof("DeleteBuldConfig selectors used for deletion: %s", selector)

	// Delete BuildConfig
	glog.V(4).Info("Deleting BuildConfigs with DeleteBuildConfig")
	return c.buildClient.BuildConfigs(c.Namespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector})
}

// generateVolumeNameFromPVC generates a random volume name based on the name
// of the given PVC
func generateVolumeNameFromPVC(pvc string) string {
	return fmt.Sprintf("%v-%v-volume", pvc, util.GenerateRandomString(nameLength))
}

// AddPVCToDeploymentConfig adds the given PVC to the given Deployment Config
// at the given path
func (c *Client) AddPVCToDeploymentConfig(dc *appsv1.DeploymentConfig, pvc string, path string) error {
	volumeName := generateVolumeNameFromPVC(pvc)

	// Validating dc.Spec.Template is present before dereferencing
	if dc.Spec.Template == nil {
		return fmt.Errorf("TemplatePodSpec in %s DeploymentConfig is empty", dc.Name)
	}
	dc.Spec.Template.Spec.Volumes = append(dc.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvc,
			},
		},
	})

	// Validating dc.Spec.Template.Spec.Containers[] is present before dereferencing
	if len(dc.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("DeploymentConfig %s doesn't have any Containers defined", dc.Name)
	}
	dc.Spec.Template.Spec.Containers[0].VolumeMounts = append(dc.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: path,
	},
	)

	glog.V(4).Infof("Updating DeploymentConfig: %v", dc)
	_, err := c.appsClient.DeploymentConfigs(c.Namespace).Update(dc)
	if err != nil {
		return errors.Wrapf(err, "failed to update DeploymentConfig: %v", dc)
	}
	return nil
}

// removeVolumeFromDC removes the volume from the given Deployment Config and
// returns true. If the given volume is not found, it returns false.
func removeVolumeFromDC(vol string, dc *appsv1.DeploymentConfig) bool {
	found := false
	for i, volume := range dc.Spec.Template.Spec.Volumes {
		if volume.Name == vol {
			found = true
			dc.Spec.Template.Spec.Volumes = append(dc.Spec.Template.Spec.Volumes[:i], dc.Spec.Template.Spec.Volumes[i+1:]...)
		}
	}
	return found
}

// removeVolumeMountFromDC removes the volumeMount from all the given containers
// in the given Deployment Config and return true. If the given volumeMount is
// not found, it returns false
func removeVolumeMountFromDC(vm string, dc *appsv1.DeploymentConfig) bool {
	found := false
	for i, container := range dc.Spec.Template.Spec.Containers {
		for j, volumeMount := range container.VolumeMounts {
			if volumeMount.Name == vm {
				found = true
				dc.Spec.Template.Spec.Containers[i].VolumeMounts = append(dc.Spec.Template.Spec.Containers[i].VolumeMounts[:j], dc.Spec.Template.Spec.Containers[i].VolumeMounts[j+1:]...)
			}
		}
	}
	return found
}

// RemoveVolumeFromDeploymentConfig removes the volume associated with the
// given PVC from the Deployment Config. Both, the volume entry and the
// volume mount entry in the containers, are deleted.
func (c *Client) RemoveVolumeFromDeploymentConfig(pvc string, dcName string) error {

	retryErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {

		dc, err := c.GetDeploymentConfigFromName(dcName)
		if err != nil {
			return errors.Wrapf(err, "unable to get Deployment Config: %v", dcName)
		}

		volumeNames := c.getVolumeNamesFromPVC(pvc, dc)
		numVolumes := len(volumeNames)
		if numVolumes == 0 {
			return fmt.Errorf("no volume found for PVC %v in DC %v, expected one", pvc, dc.Name)
		} else if numVolumes > 1 {
			return fmt.Errorf("found more than one volume for PVC %v in DC %v, expected one", pvc, dc.Name)
		}
		volumeName := volumeNames[0]

		// Remove volume if volume exists in Deployment Config
		if !removeVolumeFromDC(volumeName, dc) {
			return fmt.Errorf("could not find volume '%v' in Deployment Config '%v'", volumeName, dc.Name)
		}
		glog.V(4).Infof("Found volume: %v in Deployment Config: %v", volumeName, dc.Name)

		// Remove volume mount if volume mount exists
		if !removeVolumeMountFromDC(volumeName, dc) {
			return fmt.Errorf("could not find volumeMount: %v in Deployment Config: %v", volumeName, dc)
		}

		_, updateErr := c.appsClient.DeploymentConfigs(c.Namespace).Update(dc)
		return updateErr
	})
	if retryErr != nil {
		return errors.Wrapf(retryErr, "updating Deployment Config %v failed", dcName)
	}
	return nil
}

// getVolumeNamesFromPVC returns the name of the volume associated with the given
// PVC in the given Deployment Config
func (c *Client) getVolumeNamesFromPVC(pvc string, dc *appsv1.DeploymentConfig) []string {
	var volumes []string
	for _, volume := range dc.Spec.Template.Spec.Volumes {

		// If PVC does not exist, we skip (as this is either EmptyDir or "shared-data" from SupervisorD
		if volume.PersistentVolumeClaim == nil {
			glog.V(4).Infof("Volume has no PVC, skipping %s", volume.Name)
			continue
		}

		// If we find the PVC, add to volumes to be returned
		if volume.PersistentVolumeClaim.ClaimName == pvc {
			volumes = append(volumes, volume.Name)
		}

	}
	return volumes
}

// GetDeploymentConfigsFromSelector returns an array of Deployment Config
// resources which match the given selector
func (c *Client) GetDeploymentConfigsFromSelector(selector string) ([]appsv1.DeploymentConfig, error) {
	dcList, err := c.appsClient.DeploymentConfigs(c.Namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list DeploymentConfigs")
	}
	return dcList.Items, nil
}

// GetServicesFromSelector returns an array of Service resources which match the
// given selector
func (c *Client) GetServicesFromSelector(selector string) ([]corev1.Service, error) {
	serviceList, err := c.kubeClient.CoreV1().Services(c.Namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, errors.Wrap(err, "unable to list Services")
	}
	return serviceList.Items, nil
}

// GetDeploymentConfigFromName returns the Deployment Config resource given
// the Deployment Config name
func (c *Client) GetDeploymentConfigFromName(name string) (*appsv1.DeploymentConfig, error) {
	glog.V(4).Infof("Getting DeploymentConfig: %s", name)
	deploymentConfig, err := c.appsClient.DeploymentConfigs(c.Namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get DeploymentConfig %s", name)
	}
	return deploymentConfig, nil

}

// GetPVCsFromSelector returns the PVCs based on the given selector
func (c *Client) GetPVCsFromSelector(selector string) ([]corev1.PersistentVolumeClaim, error) {
	pvcList, err := c.kubeClient.CoreV1().PersistentVolumeClaims(c.Namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get PVCs for selector: %v", selector)
	}

	return pvcList.Items, nil
}

// GetPVCNamesFromSelector returns the PVC names for the given selector
func (c *Client) GetPVCNamesFromSelector(selector string) ([]string, error) {
	pvcs, err := c.GetPVCsFromSelector(selector)
	if err != nil {
		return nil, errors.Wrap(err, "unable to get PVCs from selector")
	}

	var names []string
	for _, pvc := range pvcs {
		names = append(names, pvc.Name)
	}

	return names, nil
}

// GetOneDeploymentConfigFromSelector returns the Deployment Config object associated
// with the given selector.
// An error is thrown when exactly one Deployment Config is not found for the
// selector.
func (c *Client) GetOneDeploymentConfigFromSelector(selector string) (*appsv1.DeploymentConfig, error) {
	deploymentConfigs, err := c.GetDeploymentConfigsFromSelector(selector)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get DeploymentConfigs for the selector: %v", selector)
	}

	numDC := len(deploymentConfigs)
	if numDC == 0 {
		return nil, fmt.Errorf("no Deployment Config was found for the selector: %v", selector)
	} else if numDC > 1 {
		return nil, fmt.Errorf("multiple Deployment Configs exist for the selector: %v. Only one must be present", selector)
	}

	return &deploymentConfigs[0], nil
}

// GetOnePodFromSelector returns the Pod  object associated with the given selector.
// An error is thrown when exactly one Pod is not found.
func (c *Client) GetOnePodFromSelector(selector string) (*corev1.Pod, error) {

	pods, err := c.kubeClient.CoreV1().Pods(c.Namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get Pod for the selector: %v", selector)
	}
	numPods := len(pods.Items)
	if numPods == 0 {
		return nil, fmt.Errorf("no Pod was found for the selector: %v", selector)
	} else if numPods > 1 {
		return nil, fmt.Errorf("multiple Pods exist for the selector: %v. Only one must be present", selector)
	}

	return &pods.Items[0], nil
}

// CopyFile copies localPath directory or list of files in copyFiles list to the directory in running Pod.
// copyFiles is list of changed files captured during `odo watch` as well as binary file path
// During copying binary components, localPath represent base directory path to binary and copyFiles contains path of binary
// During copying local source components, localPath represent base directory path whereas copyFiles is empty
// During `odo watch`, localPath represent base directory path whereas copyFiles contains list of changed Files
func (c *Client) CopyFile(localPath string, targetPodName string, targetPath string, copyFiles []string) error {
	isSingleFileTransfer := isSingleFileTransfer(copyFiles)

	dest := path.Join(targetPath, filepath.Base(localPath))
	reader, writer := io.Pipe()
	// inspired from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubectl/cmd/cp.go#L235
	go func() {
		defer writer.Close()

		var err error
		if isSingleFileTransfer {
			onlyFile := copyFiles[0]
			err = makeTar(onlyFile, targetPath+"/"+path.Base(onlyFile), writer, []string{})
		} else {
			err = makeTar(localPath, dest, writer, copyFiles)
		}
		if err != nil {
			glog.Errorf("Error while creating tar: %#v", err)
			os.Exit(-1)
		}

	}()

	// cmdArr will run inside container
	cmdArr := []string{"tar", "xf", "-", "-C", targetPath}
	if !isSingleFileTransfer {
		cmdArr = append(cmdArr, "--strip", "1")
	}

	err := c.ExecCMDInContainer(targetPodName, cmdArr, writer, writer, reader, false)
	if err != nil {
		return err
	}
	return nil
}

// isSingleFileTransfer returns true if copyFiles
// contains a single, non-directory file
func isSingleFileTransfer(copyFiles []string) bool {
	if len(copyFiles) == 1 {
		if stat, err := os.Lstat(copyFiles[0]); err == nil {
			if !stat.IsDir() {
				return true
			}
		}
	}
	return false
}

// checkFileExist check if given file exists or not
func checkFileExist(fileName string) bool {
	_, err := os.Stat(fileName)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

// makeTar function is copied from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubectl/cmd/cp.go#L309
// srcPath is ignored if files is set
func makeTar(srcPath, destPath string, writer io.Writer, files []string) error {
	// TODO: use compression here?
	tarWriter := taro.NewWriter(writer)
	defer tarWriter.Close()
	srcPath = path.Clean(srcPath)
	destPath = path.Clean(destPath)

	if len(files) != 0 {
		//watchTar
		for _, fileName := range files {
			if checkFileExist(fileName) {
				// The file could be a regular file or even a folder, so use recursiveTar which handles symlinks, regular files and folders
				return recursiveTar(path.Dir(srcPath), path.Base(srcPath), path.Dir(destPath), path.Base(destPath), tarWriter)

			}
		}
	} else {
		return recursiveTar(path.Dir(srcPath), path.Base(srcPath), path.Dir(destPath), path.Base(destPath), tarWriter)
	}

	return nil
}

// Tar will be used to tar files using odo watch
// inspired from https://gist.github.com/jonmorehouse/9060515
func tar(tw *taro.Writer, fileName string, destFile string) error {
	stat, _ := os.Lstat(fileName)

	// now lets create the header as needed for this file within the tarball
	hdr, err := taro.FileInfoHeader(stat, fileName)
	if err != nil {
		return err
	}
	splitFileName := strings.Split(fileName, destFile)[1]

	// hdr.Name can have only '/' as path separator, next line makes sure there is no '\'
	// in hdr.Name on Windows by replacing '\' to '/' in splitFileName. destFile is
	// a result of path.Base() call and never have '\' in it.
	hdr.Name = destFile + strings.Replace(splitFileName, "\\", "/", -1)
	// write the header to the tarball archive
	err = tw.WriteHeader(hdr)
	if err != nil {
		return err
	}

	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer file.Close()

	// copy the file data to the tarball
	_, err = io.Copy(tw, file)
	if err != nil {
		return err
	}

	return nil
}

// recursiveTar function is copied from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubectl/cmd/cp.go#L319
func recursiveTar(srcBase, srcFile, destBase, destFile string, tw *taro.Writer) error {
	filepath := path.Join(srcBase, srcFile)
	stat, err := os.Lstat(filepath)
	if err != nil {
		return err
	}
	if stat.IsDir() {
		files, err := ioutil.ReadDir(filepath)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			//case empty directory
			hdr, _ := taro.FileInfoHeader(stat, filepath)
			hdr.Name = destFile
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := recursiveTar(srcBase, path.Join(srcFile, f.Name()), destBase, path.Join(destFile, f.Name()), tw); err != nil {
				return err
			}
		}
		return nil
	} else if stat.Mode()&os.ModeSymlink != 0 {
		//case soft link
		hdr, _ := taro.FileInfoHeader(stat, filepath)
		target, err := os.Readlink(filepath)
		if err != nil {
			return err
		}

		hdr.Linkname = target
		hdr.Name = destFile
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
	} else {
		//case regular file or other file type like pipe
		hdr, err := taro.FileInfoHeader(stat, filepath)
		if err != nil {
			return err
		}
		hdr.Name = destFile

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(filepath)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return f.Close()
	}
	return nil
}

// GetOneServiceFromSelector returns the Service object associated with the
// given selector.
// An error is thrown when exactly one Service is not found for the selector
func (c *Client) GetOneServiceFromSelector(selector string) (*corev1.Service, error) {
	services, err := c.GetServicesFromSelector(selector)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get services for the selector: %v", selector)
	}

	numServices := len(services)
	if numServices == 0 {
		return nil, fmt.Errorf("no Service was found for the selector: %v", selector)
	} else if numServices > 1 {
		return nil, fmt.Errorf("multiple Services exist for the selector: %v. Only one must be present", selector)
	}

	return &services[0], nil
}

// AddEnvironmentVariablesToDeploymentConfig adds the given environment
// variables to the only container in the Deployment Config and updates in the
// cluster
func (c *Client) AddEnvironmentVariablesToDeploymentConfig(envs []corev1.EnvVar, dc *appsv1.DeploymentConfig) error {
	numContainers := len(dc.Spec.Template.Spec.Containers)
	if numContainers != 1 {
		return fmt.Errorf("expected exactly one container in Deployment Config %v, got %v", dc.Name, numContainers)
	}

	dc.Spec.Template.Spec.Containers[0].Env = append(dc.Spec.Template.Spec.Containers[0].Env, envs...)

	_, err := c.appsClient.DeploymentConfigs(c.Namespace).Update(dc)
	if err != nil {
		return errors.Wrapf(err, "unable to update Deployment Config %v", dc.Name)
	}
	return nil
}

// serverInfo contains the fields that contain the server's information like
// address, OpenShift and Kubernetes versions
type serverInfo struct {
	Address           string
	OpenShiftVersion  string
	KubernetesVersion string
}

// GetServerVersion will fetch the Server Host, OpenShift and Kubernetes Version
// It will be shown on the execution of odo version command
func (c *Client) GetServerVersion() (*serverInfo, error) {
	var info serverInfo

	// This will fetch the information about Server Address
	config, err := c.KubeConfig.ClientConfig()
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get server's address")
	}
	info.Address = config.Host

	// checking if the server is reachable
	if !isServerUp(config.Host) {
		return nil, errors.New("Unable to connect to OpenShift cluster, is it down?")
	}

	// This will fetch the information about OpenShift Version
	rawOpenShiftVersion, err := c.kubeClient.CoreV1().RESTClient().Get().AbsPath("/version/openshift").Do().Raw()
	if err != nil {
		// when using Minishift (or plain 'oc cluster up' for that matter) with OKD 3.11, the version endpoint is missing...
		glog.V(4).Infof("Unable to get OpenShift Version - endpoint '/version/openshift' doesn't exist")
	} else {
		var openShiftVersion version.Info
		if err := json.Unmarshal(rawOpenShiftVersion, &openShiftVersion); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal OpenShift version %v", string(rawOpenShiftVersion))
		}
		info.OpenShiftVersion = openShiftVersion.GitVersion
	}

	// This will fetch the information about Kubernetes Version
	rawKubernetesVersion, err := c.kubeClient.CoreV1().RESTClient().Get().AbsPath("/version").Do().Raw()
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get Kubernetes Version")
	}
	var kubernetesVersion version.Info
	if err := json.Unmarshal(rawKubernetesVersion, &kubernetesVersion); err != nil {
		return nil, errors.Wrapf(err, "unable to unmarshal Kubernetes Version: %v", string(rawKubernetesVersion))
	}
	info.KubernetesVersion = kubernetesVersion.GitVersion

	return &info, nil
}

// ExecCMDInContainer execute command in first container of a pod
func (c *Client) ExecCMDInContainer(podName string, cmd []string, stdout io.Writer, stderr io.Writer, stdin io.Reader, tty bool) error {

	req := c.kubeClient.CoreV1().RESTClient().
		Post().
		Namespace(c.Namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   stdin != nil,
			Stdout:  stdout != nil,
			Stderr:  stderr != nil,
			TTY:     tty,
		}, scheme.ParameterCodec)

	config, err := c.KubeConfig.ClientConfig()
	if err != nil {
		return errors.Wrapf(err, "unable to get Kubernetes client config")
	}

	// Connect to url (constructed from req) using SPDY (HTTP/2) protocol which allows bidirectional streams.
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return errors.Wrapf(err, "unable execute command via SPDY")
	}
	// initialize the transport of the standard shell streams
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    tty,
	})
	if err != nil {
		return errors.Wrapf(err, "error while streaming command")
	}

	return nil
}

// GetVolumeMountsFromDC returns a list of all volume mounts in the given DC
func (c *Client) GetVolumeMountsFromDC(dc *appsv1.DeploymentConfig) []corev1.VolumeMount {
	var volumeMounts []corev1.VolumeMount
	for _, container := range dc.Spec.Template.Spec.Containers {
		volumeMounts = append(volumeMounts, container.VolumeMounts...)
	}
	return volumeMounts
}

// IsVolumeAnEmptyDir returns true if the volume is an EmptyDir, false if not
func (c *Client) IsVolumeAnEmptyDir(volumeMountName string, dc *appsv1.DeploymentConfig) bool {
	for _, volume := range dc.Spec.Template.Spec.Volumes {
		if volume.Name == volumeMountName {
			if volume.EmptyDir != nil {
				return true
			}
		}
	}
	return false
}

// GetPVCNameFromVolumeMountName returns the PVC associated with the given volume
// An empty string is returned if the volume is not found
func (c *Client) GetPVCNameFromVolumeMountName(volumeMountName string, dc *appsv1.DeploymentConfig) string {
	for _, volume := range dc.Spec.Template.Spec.Volumes {
		if volume.Name == volumeMountName {
			if volume.PersistentVolumeClaim != nil {
				return volume.PersistentVolumeClaim.ClaimName
			}
		}
	}
	return ""
}

// GetPVCFromName returns the PVC of the given name
func (c *Client) GetPVCFromName(pvcName string) (*corev1.PersistentVolumeClaim, error) {
	return c.kubeClient.CoreV1().PersistentVolumeClaims(c.Namespace).Get(pvcName, metav1.GetOptions{})
}

// UpdatePVCLabels updates the given PVC with the given labels
func (c *Client) UpdatePVCLabels(pvc *corev1.PersistentVolumeClaim, labels map[string]string) error {
	pvc.Labels = labels
	_, err := c.kubeClient.CoreV1().PersistentVolumeClaims(c.Namespace).Update(pvc)
	if err != nil {
		return errors.Wrap(err, "unable to remove storage label from PVC")
	}
	return nil
}

// getContainerPortsFromStrings generates ContainerPort values from the array of string port values
// ports is the array containing the string port values
func getContainerPortsFromStrings(ports []string) ([]corev1.ContainerPort, error) {
	var containerPorts []corev1.ContainerPort
	for _, port := range ports {
		splits := strings.Split(port, "/")
		if len(splits) < 1 || len(splits) > 2 {
			return nil, errors.Errorf("unable to parse the port string %s", port)
		}

		portNumberI64, err := strconv.ParseInt(splits[0], 10, 32)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid port number %s", splits[0])
		}
		portNumber := int32(portNumberI64)

		var portProto corev1.Protocol
		if len(splits) == 2 {
			switch strings.ToUpper(splits[1]) {
			case "TCP":
				portProto = corev1.ProtocolTCP
			case "UDP":
				portProto = corev1.ProtocolUDP
			default:
				return nil, fmt.Errorf("invalid port protocol %s", splits[1])
			}
		} else {
			portProto = corev1.ProtocolTCP
		}

		port := corev1.ContainerPort{
			Name:          fmt.Sprintf("%d-%s", portNumber, strings.ToLower(string(portProto))),
			ContainerPort: portNumber,
			Protocol:      portProto,
		}
		containerPorts = append(containerPorts, port)
	}
	return containerPorts, nil
}

// CreateBuildConfig creates a buildConfig using the builderImage as well as gitURL.
// envVars is the array containing the environment variables
func (c *Client) CreateBuildConfig(commonObjectMeta metav1.ObjectMeta, builderImage string, gitURL string, envVars []corev1.EnvVar) (buildv1.BuildConfig, error) {

	// Retrieve the namespace, image name and the appropriate tag
	imageNS, imageName, imageTag, _, err := ParseImageName(builderImage)
	if err != nil {
		return buildv1.BuildConfig{}, errors.Wrap(err, "unable to parse image name")
	}
	imageStream, err := c.GetImageStream(imageNS, imageName, imageTag)
	if err != nil {
		return buildv1.BuildConfig{}, errors.Wrap(err, "unable to retrieve image stream for CreateBuildConfig")
	}
	imageNS = imageStream.ObjectMeta.Namespace

	glog.V(4).Infof("Using namespace: %s for the CreateBuildConfig function", imageNS)

	// Use BuildConfig to build the container with Git
	bc := generateBuildConfig(commonObjectMeta, gitURL, imageName+":"+imageTag, imageNS)

	if len(envVars) > 0 {
		bc.Spec.Strategy.SourceStrategy.Env = envVars
	}
	_, err = c.buildClient.BuildConfigs(c.Namespace).Create(&bc)
	if err != nil {
		return buildv1.BuildConfig{}, errors.Wrapf(err, "unable to create BuildConfig for %s", commonObjectMeta.Name)
	}

	return bc, nil
}

// findContainer finds the container
func findContainer(containers []corev1.Container, name string) (corev1.Container, error) {

	if name == "" {
		return corev1.Container{}, errors.New("Invalid parameter for findContainer, unable to find a blank container")
	}

	for _, container := range containers {
		if container.Name == name {
			return container, nil
		}
	}

	return corev1.Container{}, errors.New("Unable to find container")
}

// getInputEnvVarsFromStrings generates corev1.EnvVar values from the array of string key=value pairs
// envVars is the array containing the key=value pairs
func getInputEnvVarsFromStrings(envVars []string) ([]corev1.EnvVar, error) {
	var inputEnvVars []corev1.EnvVar
	var keys = make(map[string]int)
	for _, env := range envVars {
		splits := strings.SplitN(env, "=", 2)
		if len(splits) < 2 {
			return nil, errors.New("invalid syntax for env, please specify a VariableName=Value pair")
		}
		_, ok := keys[splits[0]]
		if ok {
			return nil, errors.Errorf("multiple values found for VariableName: %s", splits[0])
		} else {
			keys[splits[0]] = 1
		}

		inputEnvVars = append(inputEnvVars, corev1.EnvVar{
			Name:  splits[0],
			Value: splits[1],
		})
	}
	return inputEnvVars, nil
}

// GetEnvVarsFromDC retrieves the env vars from the DC
// dcName is the name of the dc from which the env vars are retrieved
// projectName is the name of the project
func (c *Client) GetEnvVarsFromDC(dcName string) ([]corev1.EnvVar, error) {
	dc, err := c.GetDeploymentConfigFromName(dcName)
	if err != nil {
		return nil, errors.Wrap(err, "error occured while retrieving the dc")
	}

	numContainers := len(dc.Spec.Template.Spec.Containers)
	if numContainers != 1 {
		return nil, fmt.Errorf("expected exactly one container in Deployment Config %v, got %v", dc.Name, numContainers)
	}

	return dc.Spec.Template.Spec.Containers[0].Env, nil
}
