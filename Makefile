GO ?= go
DOCKER ?= docker
SCENARIO ?= .
E2E_PARALLEL ?= 2
.PHONY: help build test test-e2e check clean smoke-up smoke-fresh smoke-alice smoke-bob smoke-down android-build android-emulator test-android android-client-build test-android-client smoke-android smoke-android-fresh smoke-android-alice smoke-android-down
help:
	@echo 'build       Build bin/keyparcel'
	@echo 'test        Run unit tests (no Docker)'
	@echo 'test-e2e    Run real SSH scenarios; SCENARIO=TestName filters'
	@echo 'check       Unit tests, vet, and end-to-end scenarios'
	@echo 'android-build    Build the minimal Android verification APK'
	@echo 'android-emulator Start the headless software emulator'
	@echo 'test-android     Verify Android against Linux (emulator must be running)'
	@echo 'android-client-build Build the Android client and UI tests'
	@echo 'test-android-client  Run the real Android UI transfer journey'
	@echo 'smoke-android    Open an interactive phone with persistent SSH fixtures'
	@echo 'smoke-android-fresh  Clear smoke app setup and open manual enrollment mode'
	@echo 'smoke-android-alice  Open the Android smoke Linux peer shell'
	@echo 'smoke-android-down   Reset only Android smoke fixture/app data'
	@echo 'smoke-up    Start persistent manual Alice/Bob environment'
	@echo 'smoke-fresh Start/reuse smoke with Alice awaiting server-key approval'
	@echo 'smoke-alice Open Alice shell (smoke-bob opens Bob)'
	@echo 'smoke-down  Remove smoke environment and its data'
	@echo 'desktop-build       Build the Linux GTK client'
	@echo 'test-desktop-client Run isolated GTK and real SSH UI tests'
	@echo 'smoke-gtk           Open persistent GTK smoke (fresh/peer/down available)'
	@echo 'Use DOCKER="sudo docker" if required by local permissions.'
build:
	CGO_ENABLED=0 $(GO) build -o bin/keyparcel ./cmd/keyparcel
test:
	$(GO) test ./...
test-e2e:
	KEYPARCEL_DOCKER='$(DOCKER)' KEYPARCEL_GO='$(GO)' $(GO) test -tags=e2e -count=1 -v -timeout=15m -parallel=$(E2E_PARALLEL) ./tests/e2e -run '$(SCENARIO)'
check: test
	$(GO) vet ./...
	$(MAKE) test-e2e
clean:
	$(GO) clean

smoke-up smoke-fresh smoke-alice smoke-bob smoke-down:
	KEYPARCEL_DOCKER='$(DOCKER)' KEYPARCEL_GO='$(GO)' bash tests/smoke/smoke.sh $(patsubst smoke-%,%,$@)

android-build:
	GO='$(GO)' bash android/build.sh
android-emulator:
	ANDROID_I_WANT_MY_TCG=yes "$(ANDROID_HOME)/emulator/emulator" -avd keyparcel-software-trial -accel off -gpu swangle -no-window -no-audio -no-boot-anim -no-metrics -no-snapshot -feature -Vulkan -port 5554
test-android: android-build
	cd android && $(GO) test ./bridge
	KEYPARCEL_DOCKER='$(DOCKER)' KEYPARCEL_GO='$(GO)' $(GO) test -tags=e2e,androidtest -count=1 -v -timeout=25m ./tests/e2e -run '^TestAndroidVerification$$'

android-client-build:
	GO='$(GO)' bash android/client/build.sh
test-android-client:
	GO='$(GO)' KEYPARCEL_DOCKER='$(DOCKER)' bash android/client/test.sh
smoke-android:
	GO='$(GO)' KEYPARCEL_DOCKER='$(DOCKER)' bash tests/smoke/android.sh up
smoke-android-fresh:
	GO='$(GO)' KEYPARCEL_DOCKER='$(DOCKER)' bash tests/smoke/android.sh fresh
smoke-android-alice:
	KEYPARCEL_DOCKER='$(DOCKER)' bash tests/smoke/android.sh alice
smoke-android-down:
	KEYPARCEL_DOCKER='$(DOCKER)' bash tests/smoke/android.sh down

.PHONY: desktop-build test-desktop-client smoke-gtk smoke-gtk-fresh smoke-gtk-peer smoke-gtk-down
desktop-build:
	GO='$(GO)' bash desktop/client/build.sh
test-desktop-client:
	KEYPARCEL_DOCKER='$(DOCKER)' python3 desktop/client/run.py test
smoke-gtk smoke-gtk-fresh:
	KEYPARCEL_DOCKER='$(DOCKER)' python3 desktop/client/run.py $(if $(filter smoke-gtk-fresh,$@),fresh,up)
smoke-gtk-peer:
	KEYPARCEL_DOCKER='$(DOCKER)' python3 desktop/client/run.py peer
smoke-gtk-down:
	KEYPARCEL_DOCKER='$(DOCKER)' python3 desktop/client/run.py down
