# Drops repeated entries from the SARIF 2.1.0 arrays whose schema says
# "uniqueItems": true, keeping the first of each and the order govulncheck
# gave. upload-sarif rejects a whole file over one repeat (issue #409).
#
# Arrays are matched by key name. Where a name is unique in one object and
# not in another (results[].locations is not; a notification's locations
# is), the other loses only exact copies, which carry nothing extra. frames
# and codeFlows are left alone: a recursive call stack repeats frames.
def unique_keys: [
  "addresses", "analysisToolLogFiles", "annotations", "artifactChanges",
  "artifacts", "attachments", "children", "contents", "conversionSources",
  "deprecatedGuids", "deprecatedIds", "deprecatedNames", "edges",
  "extensions", "fixes", "graphTraversals", "graphs", "invocations", "kinds",
  "locations", "logicalLocations", "newlineSequences", "nodes",
  "notificationConfigurationOverrides", "notifications", "policies",
  "rectangles", "redactionTokens", "regions", "relatedLocations",
  "relationships", "responseFiles", "roles", "ruleConfigurationOverrides",
  "rules", "runAggregates", "stacks", "suppressions", "supportedTaxonomies",
  "tags", "taxa", "taxonomies", "threadFlowLocations", "translations",
  "versionControlProvenance", "webRequests", "webResponses", "workItemUris"
];

def first_of_each: reduce .[] as $x ([]; if any(.[]; . == $x) then . else . + [$x] end);

unique_keys as $keys
| walk(
    if type == "object" then
      with_entries(
        if (.key | IN($keys[])) and (.value | type) == "array"
        then .value |= first_of_each
        else . end)
    else . end)
