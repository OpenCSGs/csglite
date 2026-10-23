import { describe, expect, it } from "vitest";
import { COMMUNITY_SITES, CUSTOM_SITE_ID, matchCommunitySite } from "./communitySites";

describe("matchCommunitySite", () => {
  it("treats the default hub and gateway as the main site", () => {
    expect(matchCommunitySite("https://hub.opencsg.com", "https://ai.space.opencsg.com")).toBe("main");
    expect(matchCommunitySite("https://opencsg.com/", "https://opencsg.com/aigateway")).toBe("main");
  });

  it("matches each community only when both URLs belong to it", () => {
    for (const site of COMMUNITY_SITES.filter((item) => item.id !== "main")) {
      expect(matchCommunitySite(site.serverURL, site.gatewayURL)).toBe(site.id);
    }
    expect(matchCommunitySite("https://openeast.opencsg.com", "https://ai.space.opencsg.com")).toBe(CUSTOM_SITE_ID);
  });

  it("keeps an unknown server as a custom site", () => {
    expect(matchCommunitySite("https://example.test", "https://example.test/aigateway")).toBe(CUSTOM_SITE_ID);
  });
});
