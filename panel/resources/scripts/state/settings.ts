import { action, Action } from 'easy-peasy';

export interface BrandingBackground {
    type: 'none' | 'image' | 'video';
    source: string;
    poster: string;
    overlay: number;
}

export interface BrandingMusic {
    enabled: boolean;
    source: string;
    volume: number;
    loop: boolean;
}

export interface BrandingLink {
    label: string;
    url: string;
    icon: string;
}

export interface BrandingDashboard {
    notice: {
        enabled: boolean;
        title: string;
        message: string;
    };
    links: BrandingLink[];
}

export interface Branding {
    logoUrl: string;
    background: BrandingBackground;
    music: BrandingMusic;
    dashboard: BrandingDashboard;
}

export interface SiteSettings {
    name: string;
    locale: string;
    recaptcha: {
        enabled: boolean;
        siteKey: string;
    };
    branding?: Branding;
}

export interface SettingsStore {
    data?: SiteSettings;
    setSettings: Action<SettingsStore, SiteSettings>;
}

const settings: SettingsStore = {
    data: undefined,

    setSettings: action((state, payload) => {
        state.data = payload;
    }),
};

export default settings;
