---
name: ios-app-store-launch
description: "Ship a Flutter/iOS app to TestFlight and the App Store using fastlane and the App Store Connect API. Covers what can be automated vs. what Apple forces through the web UI, plus the non-obvious failure modes (silent permission no-ops, SPM/CocoaPods conflicts, screenshot dimensions, deployment-target regressions). Triggers on: App Store, App Store Connect, TestFlight, submit for review, fastlane, ios release, provisioning profile, app store metadata, app privacy, iOS deploy."
---

# iOS App Store launch

Hard-won reference from a real first-time iOS launch of a Flutter app. Read the
**Gotchas** section before debugging anything — most time lost on that launch went
to failures that were silent or misattributed.

## Automate vs. manual

### Automatable (verified working with an App Store Connect API key)

| Task | How |
|---|---|
| Distribution cert + provisioning profile | fastlane `cert` + `sigh` with `api_key:` — no Xcode account sign-in needed |
| Code signing config | `update_code_signing_settings` — **must** scope `build_configurations: ["Release"]` |
| Build, archive, upload to TestFlight | `build_app` + `upload_to_testflight` |
| Build number increment | `latest_testflight_build_number(...) + 1` |
| Name, subtitle, description, keywords, promo text, support/marketing/privacy URL | `deliver` with `fastlane/metadata/<locale>/*.txt` |
| Screenshots upload | `deliver` (capture is separate — see Screenshots) |
| Primary/secondary category | ASC API `PATCH appInfos/{id}` relationship `primaryCategory` |
| App Review Info (contact, demo account, notes) | ASC API `PATCH appStoreReviewDetails/{id}` |
| Copyright | ASC API `PATCH appStoreVersions/{id}` attr `copyright` |
| Content rights declaration | ASC API `PATCH apps/{id}` attr `contentRightsDeclaration` |
| App price (incl. Free) | ASC API `POST appPriceSchedules` (replaces schedule; can't PATCH) |
| Attach a build to a version | ASC API `PATCH appStoreVersions/{id}` relationship `build` |
| Export compliance | Put `ITSAppUsesNonExemptEncryption=false` in `Info.plist` — stops the per-upload prompt entirely |

### Manual — web UI only

| Task | Why |
|---|---|
| **App Privacy / data collection** | No API. Every candidate endpoint 404s. Confirmed. |
| **Apple Developer Program enrollment** | Includes the **Paid Applications Agreement** — until it's Active, *no* IAP returns data anywhere |
| **App record creation** | fastlane `produce` needs username + 2FA; it does **not** accept API-key-only auth |
| **Bundle ID + capabilities** (e.g. Sign In with Apple) | Done in Certificates, Identifiers & Profiles |
| **Sandbox tester accounts** | Users and Access → Sandbox |

### Untested — verify before relying on

- Age rating **write** via API (read works: `appInfos/{id}/ageRatingDeclaration`)
- Subscription creation via API (the ASC API has endpoints; the reference launch did it in the UI)
- `submit_for_review: true` in `deliver`

## Order of operations

Dependencies matter — several steps are blocked until an earlier one lands.

1. Enroll in Developer Program → **sign Paid Applications Agreement**
2. Register bundle ID + capabilities
3. Create app record in App Store Connect (manual)
4. Generate API key (Users and Access → Integrations → **Admin** access)
5. Wire up fastlane (see `Fastfile.template`)
6. First TestFlight upload
7. Metadata + screenshots + category + copyright + content rights + price
8. App Privacy questionnaire (manual) + age rating
9. App Review Info **with demo account** if the app requires sign-in
10. Subscriptions (if any) — see Subscriptions below
11. Run `asc_audit.rb` → fix what it flags → Submit for Review

## Gotchas

### permission_handler compiles permissions OUT by default
`PermissionHandlerEnums.h` does `#ifndef PERMISSION_CAMERA / #define PERMISSION_CAMERA 0`.
Without Podfile macros, `Permission.camera.request()` hits an **empty stub**: no native
prompt, no row in iOS Settings, no error. Symptom looks like a denied permission.

Add to `ios/Podfile` `post_install` — enable only what you use, explicitly zero the rest
(unused permission APIs without usage descriptions invite review rejection):

```ruby
PERMISSION_MACROS = [
  'PERMISSION_CAMERA=1', 'PERMISSION_PHOTOS=1',
  'PERMISSION_EVENTS=0', 'PERMISSION_EVENTS_FULL_ACCESS=0', 'PERMISSION_REMINDERS=0',
  'PERMISSION_CONTACTS=0', 'PERMISSION_MICROPHONE=0', 'PERMISSION_SPEECH_RECOGNIZER=0',
  'PERMISSION_LOCATION=0', 'PERMISSION_LOCATION_WHENINUSE=0', 'PERMISSION_NOTIFICATIONS=0',
  'PERMISSION_MEDIA_LIBRARY=0', 'PERMISSION_SENSORS=0', 'PERMISSION_BLUETOOTH=0',
  'PERMISSION_APP_TRACKING_TRANSPARENCY=0', 'PERMISSION_CRITICAL_ALERTS=0', 'PERMISSION_ASSISTANT=0',
].freeze

post_install do |installer|
  installer.pods_project.targets.each do |target|
    flutter_additional_ios_build_settings(target)
    target.build_configurations.each do |config|
      existing = config.build_settings['GCC_PREPROCESSOR_DEFINITIONS'] || ['$(inherited)']
      existing = [existing] if existing.is_a?(String)
      config.build_settings['GCC_PREPROCESSOR_DEFINITIONS'] = existing + PERMISSION_MACROS
    end
  end
end
```

Verify it actually compiled in:
```bash
nm -a build/ios/iphoneos/Runner.app/Runner | grep AudioVideoPermissionStrategy
```
Real method symbols = enabled. Nothing = still stubbed out.

### Never request permissions at app launch
Eager `Permission.x.request()` in `main()` is an App Store rejection risk (Guideline 5.1.1)
and bad UX. Request in context, at the point of use.

### Flutter regenerates the SPM manifest with a stale deployment target
`flutter pub get` / `pod install` rewrite
`ios/Flutter/ephemeral/Packages/FlutterGeneratedPluginSwiftPackage/Package.swift` using a
default (often 13.0), ignoring your `IPHONEOS_DEPLOYMENT_TARGET`. Any SPM package needing
a higher minimum then fails the build. Only `flutter build ios` regenerates it correctly.

Fix: run `flutter build ios --release --no-codesign` from the Fastfile **before** `build_app`.

### Shelling out to flutter from fastlane breaks CocoaPods
`bundle exec fastlane` sets bundler env vars that break flutter's internal `pod install`
("CocoaPods is installed but broken"). Wrap it:

```ruby
Bundler.with_unbundled_env do
  Dir.chdir("..") { sh("flutter build ios --release --no-codesign") }
end
```

### update_code_signing_settings breaks simulator builds
Without `build_configurations: ["Release"]` it applies manual/distribution signing to
Debug and Profile too. Simulator runs then fail with **"Runner cannot be opened because of
a problem"** — a device-only provisioning profile can't be used on Simulator.

### SPM + CocoaPods hybrid conflicts
Two failure shapes, both from plugins being split across dependency managers:

- **"Unable to find a specification for X"** — plugin A (CocoaPods-only) depends via podspec
  on plugin B, but B has a `Package.swift` so Flutter skipped installing it as a pod.
  Fix by upgrading A to a version that also supports SPM, so both route the same way.
- **Duplicate symbols at link time** (e.g. `GTMSessionFetcher`) — the same native library
  arrives via both SPM and CocoaPods. Same fix: align plugin versions so it comes from one.

Disabling SPM globally is usually not an option — some plugins (e.g. Adapty 4.x) are
SPM-only with no podspec.

### Screenshot dimensions
App Store Connect wants exact pixel sizes; modern simulators don't match older slots.

| Slot | Accepted | Simulator that matches |
|---|---|---|
| 6.5" | 1242×2688 / 1284×2778 | iPhone 14 Plus, 14/13/12 Pro Max (1284×2778); XS Max, 11 Pro Max (1242×2688) |
| 6.9" | 1290×2796 / 1320×2868 | iPhone 16 Plus (1290×2796), 17 Pro Max (1320×2868) |

Create an older simulator if needed:
```bash
xcrun simctl create "SS-iPhone14Plus" \
  "com.apple.CoreSimulator.SimDeviceType.iPhone-14-Plus" \
  "com.apple.CoreSimulator.SimRuntime.iOS-26-5"
```
Resizing with `sips -z <h> <w>` is acceptable — aspect ratios differ by <0.5%.

Apple generally auto-scales from the 6.9" set, so uploading only 6.9" often satisfies
everything. Check which slots your version page actually shows.

### iPad support forces iPad screenshots
`TARGETED_DEVICE_FAMILY = "1,2"` (Flutter's default) makes iPad screenshots mandatory.
If iPhone-only, set `"1"` in all three Runner configs and rebuild. Verify:
```bash
/usr/libexec/PlistBuddy -c "Print :UIDeviceFamily" build/ios/iphoneos/Runner.app/Info.plist
```

### Support URL must be a real URL
`mailto:` is rejected by the ASC API. Use an https page.

### deliver crashes with "No data"
`upload_to_app_store` throws `RuntimeError: No data` from `fetch_app_store_review_detail`
if App Review Information was never created. Set review details first (UI or API).
Localized metadata usually uploads *before* the crash, so check before re-running.

### Keep the .p8 out of git
```gitignore
**/fastlane/*.p8
**/fastlane/report.xml
ios/fastlane/certs/
ios/fastlane/profiles/
```

## Subscriptions

- The **first** auto-renewable subscription must be submitted together with an app version.
- A **review screenshot is mandatory** before "Add for Review" will even enable — it must
  show the real paywall including price.
- Paywall SDKs (Adapty, RevenueCat) need their own App Store Connect API key **plus** an
  In-App Purchase key (Users and Access → **In-App Purchase** tab — a different key type
  from the App Store Connect API tab).
- Sandbox storefront/currency can resolve to the wrong country even when the Apple ID,
  device region, and sandbox tester territory are all correct. Adapty's Profiles view
  exposes `Store Country` vs `IP Country`, which is the fastest way to see it. Verify
  against a real production purchase before assuming it's sandbox-only.

## Pre-submission audit

`asc_audit.rb` checks everything the API exposes. Run from the `ios/` directory:

```bash
bundle exec ruby ~/.claude/skills/ios-app-store-launch/asc_audit.rb \
  --bundle-id com.example.app --key-id ABC123 --issuer-id <uuid> --key-path fastlane/AuthKey_ABC123.p8
```

Fields it verifies: category, age rating, build + processing state, description, keywords,
support URL, promo text, screenshots (incl. asset upload state), review contact, demo
account, review notes, copyright, content rights, app price, subscription state.

**It cannot check App Privacy** — no API. Confirm that in the UI manually.
