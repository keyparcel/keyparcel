plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

fun gitOutput(vararg args: String): String? = runCatching {
    val output = providers.exec {
        workingDir(rootProject.file("../.."))
        commandLine("git", *args)
        isIgnoreExitValue = true
    }
    if (output.result.get().exitValue == 0) output.standardOutput.asText.get().trim() else null
}.getOrNull()

val sourceRevision = providers.gradleProperty("sourceRevision").orNull ?: run {
    val revision = gitOutput("rev-parse", "HEAD")
    val status = gitOutput("status", "--porcelain")
    if (revision == null || status == null) "unknown" else revision + if (status.isEmpty()) "" else "-dirty"
}

val licenseAssets = tasks.register<Sync>("prepareLicenseAssets") {
    from(rootProject.file("../../")) {
        include("LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.txt")
        into("licenses")
    }
    into(layout.buildDirectory.dir("generated/licenseAssets"))
}
tasks.named("preBuild").configure { dependsOn(licenseAssets) }

android {
    namespace = "dev.keyparcel.app"
    compileSdk = 35
    defaultConfig {
        applicationId = "io.github.keyparcel.keyparcel"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        manifestPlaceholders["sourceRevision"] = sourceRevision
        buildConfigField("String", "SOURCE_REVISION", "\"$sourceRevision\"")
    }
    flavorDimensions += "use"
    productFlavors {
        create("production") {
            dimension = "use"
            versionName = providers.gradleProperty("releaseVersion").getOrElse("development")
            versionCode = providers.gradleProperty("releaseVersionCode").getOrElse("1").toInt()
            ndk { abiFilters += "arm64-v8a" }
        }
        create("smoke") { dimension = "use"; applicationIdSuffix = ".smoke" }
        create("scenario") { dimension = "use"; applicationIdSuffix = ".scenario" }
    }
    if (gradle.startParameter.taskNames.any { it.contains("ProductionRelease", ignoreCase = true) }) {
        require(providers.gradleProperty("releaseVersion").isPresent && providers.gradleProperty("releaseVersionCode").isPresent) {
            "Production release requires releaseVersion and releaseVersionCode"
        }
    }
    sourceSets["main"].assets.srcDir(layout.buildDirectory.dir("generated/licenseAssets"))
    sourceSets["main"].java.srcDir("../../src/dev/keyparcel/storage")
    buildFeatures { compose = true; buildConfig = true }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2025.04.01")
    implementation(composeBom)
    implementation(files(providers.gradleProperty("keyparcelAar").getOrElse("../../build/keyparcel.aar")))
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.7")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.8.7")
    implementation("androidx.compose.material3:material3")
    androidTestImplementation(composeBom)
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
    testImplementation("junit:junit:4.13.2")
}
