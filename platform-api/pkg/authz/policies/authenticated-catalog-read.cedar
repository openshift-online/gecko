permit (
    principal,
    action in [
        Action::"GetVersion",
        Action::"ListVersions",
        Action::"GetChannel",
        Action::"ListChannels"
    ],
    resource in PlatformScope::"default"
)
when {
    (resource.resourceType == "versions" &&
        action in [Action::"GetVersion", Action::"ListVersions"]) ||
    (resource.resourceType == "channels" &&
        action in [Action::"GetChannel", Action::"ListChannels"])
};
