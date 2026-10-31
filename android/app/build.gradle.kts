plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// CI passes the build number and git version; local builds get "dev".
val versionCodeEnv = System.getenv("PLEXODUS_VERSION_CODE")?.toIntOrNull() ?: 1
val versionNameEnv = System.getenv("PLEXODUS_VERSION_NAME") ?: "dev"

android {
    namespace = "app.plexodus"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.plexodus"
        minSdk = 26
        targetSdk = 35
        versionCode = versionCodeEnv
        versionName = versionNameEnv
        ndk {
            // Phones and tablets (arm64), older phones and Fire TV sticks (armv7), emulators (x86_64).
            abiFilters += listOf("arm64-v8a", "armeabi-v7a", "x86_64")
        }
    }

    // A committed key so updates install over each other. It only vouches that
    // the APK came from this repo's CI; see android/README.md to use your own.
    signingConfigs {
        create("release") {
            storeFile = rootProject.file("keystore/plexodus.jks")
            storePassword = "plexodus"
            keyAlias = "plexodus"
            keyPassword = "plexodus"
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("release")
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
