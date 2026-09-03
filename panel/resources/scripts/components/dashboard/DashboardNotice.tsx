import React from 'react';
import { useStoreState } from 'easy-peasy';
import tw from 'twin.macro';

/**
 * Dashboard welcome banner ([ SELAMAT DATANG ] style) with owner-configurable
 * quick links (channel / website / group / order, ...).
 *
 * Everything is data-driven from config/branding.php; nothing is hardcoded
 * here and unknown owner links are never invented by the panel itself.
 */
export default () => {
    const notice = useStoreState((state) => state.settings.data?.branding?.dashboard?.notice);
    const links = useStoreState((state) => state.settings.data?.branding?.dashboard?.links) || [];

    if (!notice?.enabled && links.length === 0) {
        return null;
    }

    return (
        <div css={tw`mb-4 rounded-xl border border-neutral-700 bg-neutral-800/60 p-4 backdrop-blur-sm`}>
            {notice?.enabled && (
                <div css={tw`text-center`}>
                    {notice.title && (
                        <p css={tw`font-mono text-lg font-bold tracking-wider text-orange-300 sm:text-xl`}>
                            [ {notice.title} ]
                        </p>
                    )}
                    {notice.message && (
                        <p css={tw`mt-1 text-sm text-neutral-300`}>{notice.message}</p>
                    )}
                </div>
            )}
            {links.length > 0 && (
                <div css={tw`mt-3 flex flex-wrap items-center justify-center gap-2`}>
                    {links.map((link) => (
                        <a
                            key={link.url}
                            href={link.url}
                            target={'_blank'}
                            rel={'noopener noreferrer'}
                            css={tw`inline-flex items-center gap-1 rounded-lg bg-neutral-700/70 px-3 py-1.5 text-xs font-medium text-neutral-100 transition-colors hover:bg-orange-500 hover:text-neutral-900 sm:text-sm`}
                        >
                            {link.icon && <img src={link.icon} alt={''} css={tw`h-4 w-4`} />}
                            {link.label}
                        </a>
                    ))}
                </div>
            )}
        </div>
    );
};
