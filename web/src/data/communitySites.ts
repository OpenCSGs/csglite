export interface CommunitySite {
  id: string;
  nameKey: string;
  descKey: string;
  serverURL: string;
  gatewayURL: string;
  serverAliases?: string[];
  gatewayAliases?: string[];
}

/** Community mappings; public portal configuration and model APIs were checked
 * on 2026-11-13. Longshu's TLS endpoint was unavailable during that check.
 * Hong Kong OCC and Whale Intelligence are
 * omitted: their token APIs do not match this client. */
export const COMMUNITY_SITES: CommunitySite[] = [
  {
    id: "main",
    nameKey: "settings.siteMain",
    descKey: "settings.siteMainDesc",
    serverURL: "https://hub.opencsg.com",
    gatewayURL: "https://opencsg.com/aigateway",
    serverAliases: ["https://opencsg.com"],
    gatewayAliases: ["https://ai.space.opencsg.com"],
  },
  {
    id: "sx",
    nameKey: "settings.siteSx",
    descKey: "settings.siteSxDesc",
    serverURL: "https://sanxia.opencsg.com",
    gatewayURL: "https://sanxia.opencsg.com/aigateway",
  },
  {
    id: "lg",
    nameKey: "settings.siteLg",
    descKey: "settings.siteLgDesc",
    serverURL: "https://modelhub.lgdg.cc",
    gatewayURL: "https://modelhub.lgdg.cc/aigateway",
  },
  {
    id: "df",
    nameKey: "settings.siteDf",
    descKey: "settings.siteDfDesc",
    serverURL: "https://openeast.opencsg.com",
    gatewayURL: "https://openeast.opencsg.com/aigateway",
  },
  {
    id: "csg",
    nameKey: "settings.siteCsg",
    descKey: "settings.siteCsgDesc",
    serverURL: "https://opencsg.io",
    gatewayURL: "https://opencsg.io/aigateway",
  },
];

export const CUSTOM_SITE_ID = "custom";

export function normalizeServiceURL(value: string): string {
  return value.trim().replace(/\/+$/, "").toLowerCase();
}

export function matchCommunitySite(
  serverURL: string,
  gatewayURL: string,
  defaults: { serverURL?: string; gatewayURL?: string } = {},
): string {
  const main = COMMUNITY_SITES[0];
  const server = normalizeServiceURL(serverURL.trim() || defaults.serverURL || main.serverURL);
  const gateway = normalizeServiceURL(gatewayURL.trim() || defaults.gatewayURL || main.gatewayURL);
  for (const site of COMMUNITY_SITES) {
    const servers = new Set([site.serverURL, ...(site.serverAliases ?? [])].map(normalizeServiceURL));
    const gateways = new Set([site.gatewayURL, ...(site.gatewayAliases ?? [])].map(normalizeServiceURL));
    if (site.id === "main") {
      const defaultServer = normalizeServiceURL(defaults.serverURL || site.serverURL);
      const defaultGateway = normalizeServiceURL(defaults.gatewayURL || site.gatewayURL);
      if (defaultServer) servers.add(defaultServer);
      if (defaultGateway) gateways.add(defaultGateway);
    }
    if (servers.has(server) && gateways.has(gateway)) return site.id;
  }
  return CUSTOM_SITE_ID;
}
