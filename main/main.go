package main

import (
	"flag"
	"os"

	boshblob "bosh/blobstore"
	boshlog "bosh/logger"
	boshsys "bosh/system"
	boshuuid "bosh/uuid"

	bpdep "boshprovisioner/deployment"
	bpdload "boshprovisioner/downloader"
	bpeventlog "boshprovisioner/eventlog"
	bpinstance "boshprovisioner/instance"
	bptplcomp "boshprovisioner/instance/templatescompiler"
	bpinstupd "boshprovisioner/instance/updater"
	bppkgscomp "boshprovisioner/packagescompiler"
	bpprov "boshprovisioner/provisioner"
	bprel "boshprovisioner/release"
	bpreljob "boshprovisioner/release/job"
	bptar "boshprovisioner/tar"
	bpvagrantvm "boshprovisioner/vm/vagrant"
)

const mainLogTag = "main"

var (
	configPathOpt = flag.String("configPath", "", "Path to configuration file")
)

func main() {
	logger, fs, runner, uuidGen := basicDeps()

	defer logger.HandlePanic("Main")

	flag.Parse()

	config, err := NewConfigFromPath(*configPathOpt, fs)
	if err != nil {
		logger.Error(mainLogTag, "Loading config %s", err.Error())
		os.Exit(1)
	}

	localBlobstore := boshblob.NewLocalBlobstore(
		fs,
		uuidGen,
		config.Blobstore.Options,
	)

	blobstore := boshblob.NewSHA1VerifiableBlobstore(localBlobstore)

	downloader := bpdload.NewDefaultMuxDownloader(blobstore, fs, logger)

	extractor := bptar.NewCmdExtractor(runner, fs, logger)

	compressor := bptar.NewCmdCompressor(runner, fs, logger)

	renderedArchivesCompiler := bptplcomp.NewRenderedArchivesCompiler(
		fs,
		runner,
		compressor,
		logger,
	)

	jobReaderFactory := bpreljob.NewReaderFactory(
		downloader,
		extractor,
		fs,
		logger,
	)

	err = fs.MkdirAll(config.ReposDir, os.ModeDir)
	if err != nil {
		logger.Error(mainLogTag, "Failed to create repos dir: %s", err.Error())
		os.Exit(1)
	}

	reposFactory := NewReposFactory(config.ReposDir, fs, downloader, blobstore, logger)

	blobstoreProvisioner := bpprov.NewBlobstoreProvisioner(
		fs,
		config.Blobstore,
		logger,
	)

	err = blobstoreProvisioner.Provision()
	if err != nil {
		logger.Error(mainLogTag, "Failed to provision blobstore: %s", err.Error())
		os.Exit(1)
	}

	templatesCompiler := bptplcomp.NewConcreteTemplatesCompiler(
		renderedArchivesCompiler,
		jobReaderFactory,
		reposFactory.NewJobsRepo(),
		reposFactory.NewTemplateToJobRepo(),
		reposFactory.NewRuntimePackagesRepo(),
		reposFactory.NewTemplatesRepo(),
		blobstore,
		logger,
	)

	eventLog := bpeventlog.NewLog(logger)

	packagesCompilerFactory := bppkgscomp.NewConcretePackagesCompilerFactory(
		reposFactory.NewPackagesRepo(),
		reposFactory.NewCompiledPackagesRepo(),
		blobstore,
		eventLog,
		logger,
	)

	updaterFactory := bpinstupd.NewFactory(
		templatesCompiler,
		packagesCompilerFactory,
		eventLog,
		logger,
	)

	releaseReaderFactory := bprel.NewReaderFactory(
		downloader,
		extractor,
		fs,
		logger,
	)

	deploymentReaderFactory := bpdep.NewReaderFactory(fs, logger)

	vagrantVMProvisionerFactory := bpvagrantvm.NewVMProvisionerFactory(
		fs,
		runner,
		config.AssetsDir,
		config.Blobstore.AsMap(),
		config.VMProvisioner,
		eventLog,
		logger,
	)

	vagrantVMProvisioner := vagrantVMProvisionerFactory.NewVMProvisioner()

	releaseCompiler := bpprov.NewReleaseCompiler(
		releaseReaderFactory,
		packagesCompilerFactory,
		templatesCompiler,
		vagrantVMProvisioner,
		eventLog,
		logger,
	)

	instanceProvisioner := bpinstance.NewProvisioner(
		updaterFactory,
		logger,
	)

	singleVMProvisionerFactory := bpprov.NewSingleVMProvisionerFactory(
		deploymentReaderFactory,
		config.DeploymentProvisioner,
		vagrantVMProvisioner,
		releaseCompiler,
		instanceProvisioner,
		eventLog,
		logger,
	)

	deploymentProvisioner := singleVMProvisionerFactory.NewSingleVMProvisioner()

	err = deploymentProvisioner.Provision()
	if err != nil {
		logger.Error(mainLogTag, "Failed to provision deployment: %s", err.Error())
		os.Exit(1)
	}
}

func basicDeps() (boshlog.Logger, boshsys.FileSystem, boshsys.CmdRunner, boshuuid.Generator) {
	logger := boshlog.NewWriterLogger(boshlog.LevelDebug, os.Stderr, os.Stderr)

	fs := boshsys.NewOsFileSystem(logger)

	runner := boshsys.NewExecCmdRunner(logger)

	uuidGen := boshuuid.NewGenerator()

	return logger, fs, runner, uuidGen
}
