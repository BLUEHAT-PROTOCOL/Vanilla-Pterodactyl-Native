import React, { useEffect, useState } from 'react';
import { useStoreState } from 'easy-peasy';
import tw from 'twin.macro';

/**
 * Optional anime image / video background for login + dashboard.
 *
 * - Configurable through config/branding.php (type none|image|video).
 * - Video: autoplay, muted, loop, playsinline, object-fit cover, poster
 *   fallback; users preferring reduced motion get the poster image instead
 *   of motion.
 * - Everything fails gracefully: on load error (or empty config) nothing is
 *   rendered and the standard solid background stays in place.
 */
export default () => {
    const branding = useStoreState((state) => state.settings.data?.branding);
    const [failed, setFailed] = useState(false);
    const [reducedMotion, setReducedMotion] = useState(false);

    useEffect(() => {
        if (!branding?.background || branding.background.type === 'none') return;
        const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
        setReducedMotion(mq.matches);
        const listener = (e: MediaQueryListEvent) => setReducedMotion(e.matches);
        mq.addEventListener?.('change', listener);
        return () => mq.removeEventListener?.('change', listener);
    }, [branding?.background?.type]);

    if (!branding?.background || branding.background.type === 'none' || failed) {
        return null;
    }

    const { type, source, poster, overlay } = branding.background;

    return (
        <div css={tw`fixed inset-0 z-0 pointer-events-none`} aria-hidden>
            {type === 'image' && (
                <img
                    src={source}
                    alt=""
                    css={tw`absolute inset-0 w-full h-full object-cover`}
                    onError={() => setFailed(true)}
                />
            )}
            {type === 'video' && (
                reducedMotion ? (
                    poster ? (
                        <div
                            css={tw`absolute inset-0 bg-cover bg-center`}
                            style={{ backgroundImage: `url("${poster}")` }}
                        />
                    ) : null
                ) : (
                    <video
                        css={tw`absolute inset-0 w-full h-full object-cover`}
                        src={source}
                        poster={poster || undefined}
                        autoPlay
                        muted
                        loop
                        playsInline
                        onError={() => setFailed(true)}
                    />
                )
            )}
            {/* readability overlay so forms/buttons stay usable on busy media */}
            {overlay > 0 && <div css={tw`absolute inset-0 bg-black`} style={{ opacity: overlay }} />}
        </div>
    );
};
