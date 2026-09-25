#!/usr/bin/env ruby
# Pre-submission audit for an App Store Connect app version.
# Reports every submission-blocking field the ASC API exposes.
#
#   bundle exec ruby asc_audit.rb --bundle-id com.example.app \
#     --key-id ABC123 --issuer-id <uuid> --key-path fastlane/AuthKey_ABC123.p8
#
# NOTE: App Privacy (data collection) is NOT exposed by the API. Check it in the UI.

require 'jwt'
require 'net/http'
require 'json'
require 'openssl'
require 'optparse'

opts = {}
OptionParser.new do |o|
  o.on('--bundle-id ID')  { |v| opts[:bundle_id] = v }
  o.on('--key-id ID')     { |v| opts[:key_id]    = v }
  o.on('--issuer-id ID')  { |v| opts[:issuer_id] = v }
  o.on('--key-path PATH') { |v| opts[:key_path]  = v }
end.parse!

missing = %i[bundle_id key_id issuer_id key_path].reject { |k| opts[k] }
abort("missing required args: #{missing.join(', ')}") unless missing.empty?
abort("key not found: #{opts[:key_path]}") unless File.exist?(opts[:key_path])

pk = OpenSSL::PKey::EC.new(File.read(opts[:key_path]))
TOKEN = JWT.encode(
  { iss: opts[:issuer_id], exp: Time.now.to_i + 900, aud: 'appstoreconnect-v1' },
  pk, 'ES256', { kid: opts[:key_id], typ: 'JWT' }
)

def get(path)
  uri = URI("https://api.appstoreconnect.apple.com/v1/#{path}")
  req = Net::HTTP::Get.new(uri)
  req['Authorization'] = "Bearer #{TOKEN}"
  res = Net::HTTP.start(uri.host, uri.port, use_ssl: true) { |h| h.request(req) }
  JSON.parse(res.body)
rescue StandardError
  {}
end

$fail = 0
def check(ok, label, detail = nil)
  $fail += 1 unless ok
  puts format('%-5s %-22s %s', ok ? 'OK' : 'FAIL', label, detail)
end

apps = get("apps?filter[bundleId]=#{opts[:bundle_id]}")['data'] || []
abort("no app found for bundle id #{opts[:bundle_id]}") if apps.empty?
app = apps.first
app_id = app['id']
puts "app: #{app.dig('attributes', 'name')} (#{app_id})\n\n"

# --- app-level
info = (get("apps/#{app_id}/appInfos?include=primaryCategory")['data'] || []).first
cat  = info&.dig('relationships', 'primaryCategory', 'data', 'id')
check(!cat.nil?, 'primaryCategory', cat)
check(!get("appInfos/#{info['id']}/ageRatingDeclaration")['data'].nil?, 'ageRating') if info
rights = app.dig('attributes', 'contentRightsDeclaration')
check(!rights.nil?, 'contentRights', rights)

# --- price
sched = get("apps/#{app_id}/appPriceSchedule").dig('data', 'id')
prices = sched ? (get("appPriceSchedules/#{sched}/manualPrices?include=appPricePoint&limit=5")['included'] || []) : []
auto   = sched ? (get("appPriceSchedules/#{sched}/automaticPrices?limit=1")['data'] || []) : []
check(prices.any? || auto.any?, 'appPrice',
      prices.first ? "#{prices.first.dig('attributes', 'customerPrice')} (#{get("appPriceSchedules/#{sched}/baseTerritory").dig('data', 'id')})" : nil)

# --- version
v = (get("apps/#{app_id}/appStoreVersions?limit=1")['data'] || []).first
unless v
  puts "\nno app store version found"
  exit(1)
end
state = v.dig('attributes', 'appStoreState')
unless state == 'PREPARE_FOR_SUBMISSION'
  puts "\nversion #{v.dig('attributes', 'versionString')} is #{state} — already submitted or live."
  puts "Fields below are read-only in this state; reported for reference only."
end
vid = v['id']
puts "\nversion #{v.dig('attributes', 'versionString')} (#{v.dig('attributes', 'appStoreState')}, release=#{v.dig('attributes', 'releaseType')})"
check(!v.dig('attributes', 'copyright').nil?, 'copyright', v.dig('attributes', 'copyright'))

b = get("appStoreVersions/#{vid}/build")['data']
bi = b ? get("builds/#{b['id']}").dig('data', 'attributes') : nil
check(!bi.nil? && bi['processingState'] == 'VALID', 'build',
      bi ? "#{bi['version']} (#{bi['processingState']})" : 'none attached')

# --- localizations
(get("appStoreVersions/#{vid}/appStoreVersionLocalizations")['data'] || []).each do |loc|
  a = loc['attributes']
  lc = a['locale']
  check(!a['description'].to_s.empty?, "description[#{lc}]", "#{a['description']&.length} chars")
  check(!a['keywords'].to_s.empty?,    "keywords[#{lc}]",    "#{a['keywords']&.length} chars")
  check(!a['supportUrl'].to_s.empty?,  "supportUrl[#{lc}]",  a['supportUrl'])
  sets = get("appStoreVersionLocalizations/#{loc['id']}/appScreenshotSets")['data'] || []
  check(sets.any?, "screenshots[#{lc}]", sets.empty? ? 'none' : nil)
  sets.each do |s|
    shots = get("appScreenshotSets/#{s['id']}/appScreenshots")['data'] || []
    incomplete = shots.count { |x| x.dig('attributes', 'assetDeliveryState', 'state') != 'COMPLETE' }
    check(shots.any? && incomplete.zero?, "  #{s.dig('attributes', 'screenshotDisplayType')}",
          "#{shots.length} uploaded#{incomplete.positive? ? ", #{incomplete} INCOMPLETE" : ''}")
  end
end

# --- review details
rd = get("appStoreVersions/#{vid}/appStoreReviewDetail")['data']
ra = rd ? rd['attributes'] : {}
check(!ra['contactEmail'].to_s.empty?, 'reviewContact',
      rd ? "#{ra['contactFirstName']} #{ra['contactLastName']} <#{ra['contactEmail']}>" : 'NOT SET')
if ra['demoAccountRequired']
  check(!ra['demoAccountName'].to_s.empty?, 'demoAccount', ra['demoAccountName'])
else
  puts format('%-5s %-22s %s', 'note', 'demoAccount', 'not required — correct only if app needs no sign-in')
end

# --- subscriptions
(get("apps/#{app_id}/subscriptionGroups")['data'] || []).each do |g|
  (get("subscriptionGroups/#{g['id']}/subscriptions")['data'] || []).each do |s|
    st = s.dig('attributes', 'state')
    check(%w[READY_TO_SUBMIT APPROVED WAITING_FOR_REVIEW IN_REVIEW].include?(st),
          'subscription', "#{s.dig('attributes', 'productId')} (#{st})")
  end
end

puts "\nNOT CHECKED (no API): App Privacy / data collection — verify in App Store Connect UI."
puts $fail.zero? ? "\nAll API-visible checks passed." : "\n#{$fail} item(s) need attention."
exit($fail.zero? ? 0 : 1)
