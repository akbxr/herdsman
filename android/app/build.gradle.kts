import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("app.cash.paparazzi") // screen snapshots on the JVM: ./gradlew recordPaparazziDebug
}

android {
    namespace = "dev.akbxr.herdsman"
    compileSdk = 34

    defaultConfig {
        applicationId = "dev.akbxr.herdsman"
        minSdk = 26
        targetSdk = 34
        // Bumped by release-please from the Conventional Commits; the code
        // follows from it (1.2.3 -> 10203) so every release installs over the last.
        versionName = "0.4.0" // x-release-please-version
        versionCode = versionName!!.split(".").map { it.takeWhile(Char::isDigit).toInt() }
            .let { (major, minor, patch) -> major * 10000 + minor * 100 + patch }
    }

    // The release key: from the environment in CI (GitHub secrets), or from
    // ~/.android/herdsman-release.properties on a machine that has it. Without
    // it a release build is signed with the debug key and won't install over a
    // published APK.
    val releaseKey = Properties().apply {
        val local = File(System.getProperty("user.home"), ".android/herdsman-release.properties")
        if (local.isFile) local.inputStream().use { load(it) }
        System.getenv("HERDSMAN_KEYSTORE")?.let { setProperty("storeFile", it) }
        System.getenv("HERDSMAN_KEYSTORE_PASSWORD")?.let { setProperty("storePassword", it) }
        System.getenv("HERDSMAN_KEY_ALIAS")?.let { setProperty("keyAlias", it) }
        System.getenv("HERDSMAN_KEY_PASSWORD")?.let { setProperty("keyPassword", it) }
    }
    val hasReleaseKey = releaseKey.getProperty("storeFile")?.let { File(it).isFile } == true
    signingConfigs {
        if (hasReleaseKey) create("release") {
            storeFile = File(releaseKey.getProperty("storeFile"))
            storePassword = releaseKey.getProperty("storePassword")
            keyAlias = releaseKey.getProperty("keyAlias")
            keyPassword = releaseKey.getProperty("keyPassword")
        }
    }

    buildTypes {
        release {
            // The build to use: R8 shrinks and optimises the code (Compose in a
            // debug build is several times slower, scrolling most of all).
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.getByName(if (hasReleaseKey) "release" else "debug")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
    }
}

composeCompiler {
    stabilityConfigurationFile = project.layout.projectDirectory.file("compose-stability.conf")
}

dependencies {
    val bom = platform("androidx.compose:compose-bom:2024.09.00")
    implementation(bom)
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-core")
    // Installs the baseline profiles Compose ships, so ART compiles the hot
    // paths ahead of time instead of interpreting them on first scrolls.
    implementation("androidx.profileinstaller:profileinstaller:1.3.1")
    // QR scanning through Google Play services: its own camera screen, so the
    // app needs no camera permission.
    implementation("com.google.android.gms:play-services-code-scanner:16.1.0")
    debugImplementation("androidx.compose.ui:ui-tooling")
    testImplementation("junit:junit:4.13.2")
}
