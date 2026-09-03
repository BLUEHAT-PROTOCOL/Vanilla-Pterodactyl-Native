<?php

namespace Pterodactyl\Http\ViewComposers;

use Illuminate\View\View;
use Pterodactyl\Services\Helpers\AssetHashService;

class AssetComposer
{
    /**
     * AssetComposer constructor.
     */
    public function __construct(private AssetHashService $assetHashService)
    {
    }

    /**
     * Provide access to the asset service in the views.
     */
    public function compose(View $view): void
    {
        $view->with('asset', $this->assetHashService);
        $view->with('siteConfiguration', [
            'name' => config('app.name') ?? 'Pterodactyl',
            'locale' => config('app.locale') ?? 'en',
            'recaptcha' => [
                'enabled' => config('recaptcha.enabled', false),
                'siteKey' => config('recaptcha.website_key') ?? '',
            ],
            'branding' => $this->branding(),
        ]);
    }

    /**
     * Public branding payload (config/branding.php). Only whitelisted,
     * public-safe fields are exposed; invalid values fall back to defaults.
     */
    private function branding(): array
    {
        $type = mb_strtolower((string) config('branding.background.type', 'none'));
        if (!in_array($type, ['none', 'image', 'video'], true)) {
            $type = 'none';
        }
        $source = (string) config('branding.background.source', '');
        if ($type !== 'none' && !$this->isPublicUrl($source)) {
            $type = 'none';
            $source = '';
        }

        $links = [];
        foreach ((array) config('branding.dashboard.links', []) as $link) {
            if (!is_array($link)) {
                continue;
            }
            $label = trim((string) ($link['label'] ?? ''));
            $url = trim((string) ($link['url'] ?? ''));
            if ($label === '' || !$this->isPublicUrl($url) || !($link['enabled'] ?? true)) {
                continue;
            }
            $links[] = [
                'label' => $label,
                'url' => $url,
                'icon' => isset($link['icon']) && $this->isPublicUrl((string) $link['icon']) ? (string) $link['icon'] : '',
            ];
        }

        return [
            'logoUrl' => $this->isPublicUrl((string) config('branding.logo_url', ''))
                ? (string) config('branding.logo_url') : '',
            'background' => [
                'type' => $type,
                'source' => $source,
                'poster' => $this->isPublicUrl((string) config('branding.background.poster', ''))
                    ? (string) config('branding.background.poster') : '',
                'overlay' => max(0.0, min(1.0, (float) config('branding.background.overlay', 0.45))),
            ],
            'music' => [
                'enabled' => (bool) config('branding.music.enabled', false)
                    && $this->isPublicUrl((string) config('branding.music.source', '')),
                'source' => (string) config('branding.music.source', ''),
                'volume' => max(0.0, min(1.0, (float) config('branding.music.volume', 0.5))),
                'loop' => (bool) config('branding.music.loop', true),
            ],
            'dashboard' => [
                'notice' => [
                    'enabled' => (bool) config('branding.dashboard.notice.enabled', false),
                    'title' => (string) config('branding.dashboard.notice.title', ''),
                    'message' => (string) config('branding.dashboard.notice.message', ''),
                ],
                'links' => $links,
            ],
        ];
    }

    /** Accept only absolute http(s) URLs or app-relative /paths (no javascript:, data:, ...). */
    private function isPublicUrl(string $url): bool
    {
        if ($url === '') {
            return false;
        }
        if (str_starts_with($url, '/') && !str_starts_with($url, '//')) {
            return true;
        }

        return (bool) preg_match('#^https?://#i', $url);
    }
}
