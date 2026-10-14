interface Window {
  ruilianDesktop?: {
    isDesktop: boolean
    quit(): Promise<void>
    restore(): Promise<boolean>
    openDataDirectory(): Promise<void>
    refreshBranding(): Promise<void>
  }
}
