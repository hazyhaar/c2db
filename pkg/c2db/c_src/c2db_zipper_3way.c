#include "c2db_zipper_3way.h"

c2db_zip_result_t c2db_zipper_3way(
    const c2db_zip_entry_t *base,   uint64_t n_base,
    const c2db_zip_entry_t *ours,   uint64_t n_ours,
    const c2db_zip_entry_t *theirs, uint64_t n_theirs,
    c2db_zip_entry_t *out,          uint64_t cap
) {
    c2db_zip_result_t res;
    uint64_t ib;
    uint64_t io;
    uint64_t it;
    uint64_t k_out;
    uint64_t conflicts;

    res.merged_count = 0;
    res.conflicts_count = 0;
    res.ok = 1;

    if (out == 0 && cap > 0) {
        res.ok = 0;
        return res;
    }
    if (base == 0 && n_base > 0) {
        res.ok = 0;
        return res;
    }
    if (ours == 0 && n_ours > 0) {
        res.ok = 0;
        return res;
    }
    if (theirs == 0 && n_theirs > 0) {
        res.ok = 0;
        return res;
    }

    ib = 0;
    io = 0;
    it = 0;
    k_out = 0;
    conflicts = 0;

    while (ib < n_base || io < n_ours || it < n_theirs) {
        uint64_t min_key;
        uint8_t has_b;
        uint8_t has_o;
        uint8_t has_t;
        c2db_zip_entry_t chosen;
        uint8_t emit;

        min_key = 0xFFFFFFFFFFFFFFFFULL;
        if (ib < n_base && base[ib].key < min_key) {
            min_key = base[ib].key;
        }
        if (io < n_ours && ours[io].key < min_key) {
            min_key = ours[io].key;
        }
        if (it < n_theirs && theirs[it].key < min_key) {
            min_key = theirs[it].key;
        }

        has_b = 0;
        if (ib < n_base && base[ib].key == min_key) {
            has_b = 1;
        }
        has_o = 0;
        if (io < n_ours && ours[io].key == min_key) {
            has_o = 1;
        }
        has_t = 0;
        if (it < n_theirs && theirs[it].key == min_key) {
            has_t = 1;
        }

        emit = 1;

        if (has_b == 0 && has_o != 0 && has_t != 0) {
            if (ours[io].val_crc == theirs[it].val_crc && ours[io].deleted == theirs[it].deleted) {
                chosen = ours[io];
            } else {
                conflicts = conflicts + 1;
                if (ours[io].ver > theirs[it].ver) {
                    chosen = ours[io];
                } else {
                    chosen = theirs[it];
                }
            }
        } else if (has_b == 0 && has_o != 0 && has_t == 0) {
            chosen = ours[io];
        } else if (has_b == 0 && has_o == 0 && has_t != 0) {
            chosen = theirs[it];
        } else if (has_b != 0 && has_o != 0 && has_t != 0) {
            uint8_t o_mut;
            uint8_t t_mut;

            o_mut = 0;
            if (ours[io].val_crc != base[ib].val_crc || ours[io].deleted != base[ib].deleted) {
                o_mut = 1;
            }
            t_mut = 0;
            if (theirs[it].val_crc != base[ib].val_crc || theirs[it].deleted != base[ib].deleted) {
                t_mut = 1;
            }

            if (o_mut == 0 && t_mut == 0) {
                chosen = base[ib];
            } else if (o_mut != 0 && t_mut == 0) {
                chosen = ours[io];
            } else if (o_mut == 0 && t_mut != 0) {
                chosen = theirs[it];
            } else {
                if (ours[io].val_crc == theirs[it].val_crc && ours[io].deleted == theirs[it].deleted) {
                    chosen = ours[io];
                } else {
                    conflicts = conflicts + 1;
                    if (ours[io].ver > theirs[it].ver) {
                        chosen = ours[io];
                    } else {
                        chosen = theirs[it];
                    }
                }
            }
        } else if (has_b != 0 && has_o != 0 && has_t == 0) {
            uint8_t o_mut;
            o_mut = 0;
            if (ours[io].val_crc != base[ib].val_crc || ours[io].deleted != base[ib].deleted) {
                o_mut = 1;
            }
            if (o_mut == 0) {
                emit = 0;
            } else {
                conflicts = conflicts + 1;
                chosen = ours[io];
            }
        } else if (has_b != 0 && has_o == 0 && has_t != 0) {
            uint8_t t_mut;
            t_mut = 0;
            if (theirs[it].val_crc != base[ib].val_crc || theirs[it].deleted != base[ib].deleted) {
                t_mut = 1;
            }
            if (t_mut == 0) {
                emit = 0;
            } else {
                conflicts = conflicts + 1;
                chosen = theirs[it];
            }
        } else {
            emit = 0;
        }

        if (emit != 0) {
            if (k_out >= cap) {
                res.ok = 0;
                res.merged_count = k_out;
                res.conflicts_count = conflicts;
                return res;
            }
            out[k_out] = chosen;
            k_out = k_out + 1;
        }

        if (has_b != 0) {
            ib = ib + 1;
        }
        if (has_o != 0) {
            io = io + 1;
        }
        if (has_t != 0) {
            it = it + 1;
        }
    }

    res.merged_count = k_out;
    res.conflicts_count = conflicts;
    res.ok = 1;
    return res;
}
