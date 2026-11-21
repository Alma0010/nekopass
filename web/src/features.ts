export type SiteFeatures = {
 commerce: boolean
 invites: boolean
 tickets: boolean
 users: boolean
}

export function withFeatureDefaults(features?: Partial<SiteFeatures> | null): SiteFeatures {
 return {
  commerce: features?.commerce !== false,
  invites: features?.invites !== false,
  tickets: features?.tickets !== false,
  users: features?.users !== false,
 }
}
