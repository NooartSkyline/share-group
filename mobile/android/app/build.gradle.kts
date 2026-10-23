plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val goAar = file("libs/lanshare.aar")

android {
    namespace = "app.lanshare.host"
    compileSdk = 34
    defaultConfig {
        applicationId = "app.lanshare.host"
        minSdk = 26
        targetSdk = 34
        versionCode = 1
        versionName = "0.1.0"
    }
    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    sourceSets {
        getByName("main") {
            if (!goAar.exists()) {
                java.srcDir("src/fallback/java")
            }
        }
    }
}

dependencies {
    if (goAar.exists()) {
        implementation(files(goAar))
    }
}
