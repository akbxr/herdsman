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
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            // The build to use: R8 shrinks and optimises the code (Compose in a
            // debug build is several times slower, scrolling most of all).
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            // Signed with the debug key so the APK installs over the debug one,
            // keeping the pairing, without extra setup.
            signingConfig = signingConfigs.getByName("debug")
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
