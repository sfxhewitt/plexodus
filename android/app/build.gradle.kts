plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// CI passes the build number and git version; local builds get "dev".
val versionCodeEnv = System.getenv("PLEXODUS_VERSION_CODE")?.toIntOrNull() ?: 1
val versionNameEnv = System.getenv("PLEXODUS_VERSION_NAME") ?: "dev"

android {
    namespace = "app.plexodus"
    compileSdk = 36

    defaultConfig {
        applicationId = "app.plexodus"
        minSdk = 26
        targetSdk = 36
        versionCode = versionCodeEnv
        versionName = versionNameEnv
        // Where the app looks for its own updates (the "latest" release).
        buildConfigField("String", "UPDATE_REPO", "\"${System.getenv("GITHUB_REPOSITORY") ?: "sfxhewitt/plexodus"}\"")
        ndk {
            // Phones and tablets (arm64), older phones and Fire TV sticks (armv7), emulators (x86_64).
            abiFilters += listOf("arm64-v8a", "armeabi-v7a", "x86_64")
        }
    }

    // CI supplies the release key through the environment (see the workflow and
    // android/README.md). Without one, release builds use the debug key so they
    // still install.
    val keystore = System.getenv("PLEXODUS_KEYSTORE")
    val keystorePassword = System.getenv("PLEXODUS_KEYSTORE_PASSWORD") ?: ""
    signingConfigs {
        create("release") {
            if (keystore != null) {
                storeFile = file(keystore)
                storePassword = keystorePassword
                keyAlias = "plexodus"
                keyPassword = keystorePassword
            }
        }
    }

    // Two ways the app reaches people:
    //  - github: the APK on the "latest" release. It updates itself (Updater.kt).
    //  - store:  Google Play (AAB) and the Amazon Appstore (APK). The store
    //            updates it, so the self-updater and the install-packages
    //            permission are left out (store policy forbids both).
    flavorDimensions += "channel"
    productFlavors {
        create("github") {
            dimension = "channel"
            buildConfigField("boolean", "SELF_UPDATE", "true")
        }
        create("store") {
            dimension = "channel"
            buildConfigField("boolean", "SELF_UPDATE", "false")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName(if (keystore != null) "release" else "debug")
        }
        debug {
            applicationIdSuffix = ".debug"
        }
    }

    buildFeatures {
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    // VLC's engine: plays every container and codec directly, no transcoding.
    implementation("org.videolan.android:libvlc-all:3.7.7")
}
