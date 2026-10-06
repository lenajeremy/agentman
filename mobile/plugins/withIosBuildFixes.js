const {
  withAppDelegate,
  withDangerousMod,
  withEntitlementsPlist,
  withInfoPlist,
  withXcodeProject,
} = require("expo/config-plugins");
const fs = require("fs");
const path = require("path");

/**
 * Fixes for the generated iOS project.
 *
 * They live here rather than in ios/ because `expo prebuild` regenerates that
 * directory from scratch — anything edited there is lost the next time anyone
 * runs it, including on a fresh clone where it does not exist at all.
 */

/** Below this, Xcode 16+ refuses to build at all. */
const MIN_DEPLOYMENT_TARGET = 15.1;

/**
 * Raise pods stuck below the minimum iOS version.
 *
 * Most pods inherit the app's target, but a few declare their own in their
 * podspec and CocoaPods honours it. AsyncStorage's resource bundle asks for
 * 13.4, which Xcode rejects outright:
 *
 *   The iOS deployment target 'IPHONEOS_DEPLOYMENT_TARGET' is set to 13.4,
 *   but the range of supported deployment target versions is 15.0 to 27.0
 *
 * Two targets out of 194, and the build fails completely.
 */
function withPodDeploymentTarget(config) {
  return withDangerousMod(config, [
    "ios",
    (config) => {
      const podfile = path.join(config.modRequest.platformProjectRoot, "Podfile");
      let contents = fs.readFileSync(podfile, "utf8");

      if (contents.includes("agentman:deployment-target")) return config;

      const anchor = ":ccache_enabled => ccache_enabled?(podfile_properties),\n    )";
      if (!contents.includes(anchor)) {
        throw new Error(
          "withIosBuildFixes: could not find the post_install hook in the Podfile. " +
            "Expo's template changed; update this plugin rather than editing ios/.",
        );
      }

      // Resource bundles can land in separately generated projects depending on
      // the CocoaPods version, so both are walked.
      contents = contents.replace(
        anchor,
        anchor +
          `

    # agentman:deployment-target — see plugins/withIosBuildFixes.js
    projects = [installer.pods_project]
    projects += installer.generated_projects if installer.respond_to?(:generated_projects)
    projects.compact.uniq.each do |project|
      project.targets.each do |target|
        target.build_configurations.each do |build_configuration|
          current = build_configuration.build_settings['IPHONEOS_DEPLOYMENT_TARGET'].to_f
          if current > 0 && current < ${MIN_DEPLOYMENT_TARGET}
            build_configuration.build_settings['IPHONEOS_DEPLOYMENT_TARGET'] = '${MIN_DEPLOYMENT_TARGET}'
          end
        end
      end
      project.save
    end`,
      );

      fs.writeFileSync(podfile, contents);
      return config;
    },
  ]);
}

/**
 * Set the signing team, so Xcode can mint a provisioning profile.
 *
 * Without one the build stops at:
 *
 *   No profiles for 'dev.agentman.app' were found
 *
 * Read from the environment rather than committed. A team id is not a secret —
 * it ships inside every app — but it belongs to one developer, and hardcoding
 * one here would mean everyone else's build silently tries to sign as them.
 *
 * A free Apple ID works: its Personal Team can sign for devices you own, with a
 * certificate that expires after seven days.
 */
function withSigningTeam(config) {
  const teamId = process.env.APPLE_TEAM_ID;
  if (!teamId) return config;

  return withXcodeProject(config, (config) => {
    const project = config.modResults;
    project.addTargetAttribute("DevelopmentTeam", teamId);

    const buildConfigurations = project.pbxXCBuildConfigurationSection();
    for (const key of Object.keys(buildConfigurations)) {
      const settings = buildConfigurations[key].buildSettings;
      // Only the app target's configurations carry PRODUCT_BUNDLE_IDENTIFIER;
      // pod configurations must be left alone.
      if (!settings || !settings.PRODUCT_BUNDLE_IDENTIFIER) continue;
      settings.DEVELOPMENT_TEAM = teamId;
      settings.CODE_SIGN_STYLE = "Automatic";
    }
    return config;
  });
}

/**
 * Drop the push-notification entitlement unless someone asks for it.
 *
 * expo-notifications adds `aps-environment` whenever it is installed, and a
 * free Apple ID cannot provision it at all:
 *
 *   Personal development teams, including "...", do not support the Push
 *   Notifications capability.
 *
 * What this costs depends on the account. Without the entitlement the app falls
 * back to *local* notifications — scheduleNotificationAsync with `trigger:
 * null`, driven by frames that already arrived over the relay websocket. Those
 * need no entitlement, but they can only fire while the socket is alive, so
 * nothing arrives once iOS suspends the app.
 *
 * Set APPLE_PUSH=1 with a paid team to keep it. That is what lets the daemon
 * reach a suspended phone through APNs, which is the case local notifications
 * cannot cover and the one users actually care about.
 */
function withoutPushEntitlement(config) {
  if (process.env.APPLE_PUSH === "1") return config;
  return withEntitlementsPlist(config, (config) => {
    delete config.modResults["aps-environment"];
    return config;
  });
}

/**
 * Adopt the scene life cycle, which iOS 27 requires.
 *
 * An app built with the iOS 27 SDK stops at launch on iOS 27 unless it uses
 * scenes: "Application failed to launch: UIScene life cycle is required for
 * apps built with this SDK" (TN3187). Builds made with Xcode 27 opened on an
 * iOS 26 phone and failed on an iOS 27 one.
 *
 * Expo ships the scene delegate (ExpoAppSceneDelegate); the app has to name it
 * in Info.plist and let it create the window. The generated AppDelegate makes
 * the window and starts React Native itself, so that part is removed and the
 * delegate declares the provider conformance the scene delegate looks for. If
 * Expo's template changes shape, this throws rather than shipping a build
 * that starts React Native twice or not at all.
 */
function withSceneLifeCycle(config) {
  config = withInfoPlist(config, (config) => {
    config.modResults.UIApplicationSceneManifest = {
      UIApplicationSupportsMultipleScenes: false,
      UISceneConfigurations: {
        UIWindowSceneSessionRoleApplication: [
          {
            UISceneConfigurationName: "Default Configuration",
            UISceneDelegateClassName: "EXExpoAppSceneDelegate",
          },
        ],
      },
    };
    return config;
  });
  return withAppDelegate(config, (config) => {
    let source = config.modResults.contents;
    if (source.includes("ExpoReactNativeFactoryProvider")) return config;
    source = source.replace(
      "class AppDelegate: ExpoAppDelegate {",
      "class AppDelegate: ExpoAppDelegate, ExpoReactNativeFactoryProvider {",
    );
    source = source.replace(
      /#if os\(iOS\) \|\| os\(tvOS\)\n\s*window = UIWindow\(frame: UIScreen\.main\.bounds\)\n\s*factory\.startReactNative\([\s\S]*?\)\n#endif\n/,
      "    // ExpoAppSceneDelegate creates the window and starts React Native in it.\n",
    );
    if (
      !source.includes("ExpoAppDelegate, ExpoReactNativeFactoryProvider") ||
      source.includes("UIWindow(frame: UIScreen.main.bounds)")
    ) {
      throw new Error(
        "withIosBuildFixes: the generated AppDelegate no longer matches; adopt the scene life cycle by hand",
      );
    }
    config.modResults.contents = source;
    return config;
  });
}

module.exports = function withIosBuildFixes(config) {
  return withSceneLifeCycle(
    withoutPushEntitlement(withSigningTeam(withPodDeploymentTarget(config))),
  );
};
