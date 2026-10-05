import AppKit
import Combine
import SwiftUI

/// Backing state for the history panel: the search query, the filtered/sectioned lists, and
/// the keyboard selection.
///
/// Selection is an index into `visible` (pinned first, then recents) rather than an item ID,
/// because ↑/↓ have to walk *across* the section boundary as if it were one list.
final class ClipboardPanelModel: ObservableObject {

    @Published var searchText: String = ""
    @Published var selectedIndex: Int = 0

    /// Bumped only by keyboard navigation.
    ///
    /// The list scrolls in response to *this*, never to `selectedIndex` directly. Scrolling on
    /// every selection change meant the view yanked itself around whenever the selection moved
    /// for any reason — the pointer must never make the list scroll itself.
    @Published private(set) var keyboardScrollTick: Int = 0

    @Published private(set) var pinned: [ClipItem] = []
    @Published private(set) var recents: [ClipItem] = []

    let store: ClipboardStore
    private var cancellables = Set<AnyCancellable>()

    init(store: ClipboardStore) {
        self.store = store

        // Re-filter whenever the query changes or the history mutates underneath us
        // (a copy landing while the panel is open should show up immediately).
        Publishers.CombineLatest($searchText, store.$items)
            .sink { [weak self] query, items in
                self?.refresh(query: query, items: items)
            }
            .store(in: &cancellables)
    }

    /// Flattened selection space. Pinned entries come first, matching the visual order.
    var visible: [ClipItem] { pinned + recents }

    var selectedItem: ClipItem? {
        guard visible.indices.contains(selectedIndex) else { return nil }
        return visible[selectedIndex]
    }

    private func refresh(query: String, items: [ClipItem]) {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let matching = needle.isEmpty
            ? items
            : items.filter { $0.searchHaystack.contains(needle) }

        pinned = matching.filter(\.isPinned)
        recents = matching.filter { !$0.isPinned }
        clampSelection()
    }

    func clampSelection() {
        if visible.isEmpty {
            selectedIndex = 0
        } else {
            selectedIndex = min(max(0, selectedIndex), visible.count - 1)
        }
    }

    // MARK: - Keyboard actions

    func moveSelection(by delta: Int) {
        guard !visible.isEmpty else { return }
        selectedIndex = (selectedIndex + delta).clamped(to: 0...(visible.count - 1))
        keyboardScrollTick += 1
    }

    func togglePinSelected() {
        guard let item = selectedItem else { return }
        store.togglePin(item)
        // Pinning moves the row into the other section; keep the same row selected by
        // following the item to its new index.
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            if let index = self.visible.firstIndex(where: { $0.id == item.id }) {
                self.selectedIndex = index
            }
        }
    }

    func removeSelected() {
        guard let item = selectedItem else { return }
        store.remove(item)
        clampSelection()
    }
}

private extension Comparable {
    func clamped(to range: ClosedRange<Self>) -> Self {
        min(max(self, range.lowerBound), range.upperBound)
    }
}

// MARK: - View

struct ClipboardHistoryView: View {

    @ObservedObject var model: ClipboardPanelModel
    /// Invoked with `plainTextOnly`.
    var onPaste: (Bool) -> Void
    var onClose: () -> Void

    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            searchBar
            Divider()
            if model.visible.isEmpty {
                emptyState
            } else {
                list
            }
            Divider()
            footer
        }
        .frame(minWidth: 380, minHeight: 320)
        .background(.regularMaterial)
    }

    // MARK: Search

    private var searchBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(.secondary)
            // The panel's key handler intercepts ↑/↓/↩/⎋ before the field sees them, so
            // typing here never fights with navigation.
            TextField("Search clipboard history", text: $model.searchText)
                .textFieldStyle(.plain)
                .font(.system(size: 13))
                .focused($searchFocused)
            if !model.searchText.isEmpty {
                Button {
                    model.searchText = ""
                } label: {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .onAppear { searchFocused = true }
    }

    // MARK: List

    private var list: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    if !model.pinned.isEmpty {
                        sectionHeader("Pinned")
                        ForEach(Array(model.pinned.enumerated()), id: \.element.id) { offset, item in
                            row(item, index: offset)
                        }
                    }
                    if !model.recents.isEmpty {
                        sectionHeader("Recent")
                        ForEach(Array(model.recents.enumerated()), id: \.element.id) { offset, item in
                            row(item, index: model.pinned.count + offset)
                        }
                    }
                }
                .padding(.vertical, 6)
                .padding(.horizontal, 8)
            }
            // Keyed to the keyboard tick, not to `selectedIndex`: scrolling belongs to ↑/↓
            // only. The user scrolls with the wheel and clicks what they want; the view must
            // never move under their pointer.
            .onChange(of: model.keyboardScrollTick) { _, _ in
                let index = model.selectedIndex
                guard model.visible.indices.contains(index) else { return }
                withAnimation(.easeOut(duration: 0.12)) {
                    proxy.scrollTo(model.visible[index].id, anchor: .center)
                }
            }
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title.uppercased())
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 6)
            .padding(.top, 6)
            .padding(.bottom, 2)
    }

    /// One row.
    ///
    /// A `Button` rather than tap gestures, deliberately. Stacking `.onTapGesture(count: 2)`
    /// and `.onTapGesture()` makes SwiftUI arbitrate between them, and inside this panel the
    /// result was that *neither* fired — rows were completely unclickable. A button's hit
    /// testing is unambiguous and works the same whether or not the panel is key.
    ///
    /// A single click pastes, matching Windows' Win+V and the `↩` shortcut. The hover
    /// highlight makes it obvious what a click is about to act on.
    private func row(_ item: ClipItem, index: Int) -> some View {
        Button {
            model.selectedIndex = index
            onPaste(false)
        } label: {
            ClipRowView(item: item,
                        store: model.store,
                        isSelected: index == model.selectedIndex)
                .contentShape(Rectangle())
        }
            .buttonStyle(.plain)
            .id(item.id)
            .contextMenu {
                Button(item.isPinned ? "Unpin" : "Pin") {
                    model.selectedIndex = index
                    model.togglePinSelected()
                }
                Button("Paste") {
                    model.selectedIndex = index
                    onPaste(false)
                }
                Button("Paste as Plain Text") {
                    model.selectedIndex = index
                    onPaste(true)
                }
                Divider()
                Button("Delete", role: .destructive) {
                    model.selectedIndex = index
                    model.removeSelected()
                }
            }
    }

    private var emptyState: some View {
        VStack(spacing: 6) {
            Spacer()
            Image(systemName: "doc.on.clipboard")
                .font(.system(size: 28))
                .foregroundStyle(.tertiary)
            Text(model.searchText.isEmpty ? "No clipboard history yet" : "No matches")
                .font(.system(size: 13))
                .foregroundStyle(.secondary)
            Spacer()
        }
        .frame(maxWidth: .infinity)
    }

    private var footer: some View {
        HStack(spacing: 12) {
            hint("Click", "Paste")
            hint("⇧↩", "Plain")
            hint("⌘P", "Pin")
            hint("⌘⌫", "Delete")
            Spacer()
            Button("Close", action: onClose)
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .font(.system(size: 11))
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private func hint(_ key: String, _ label: String) -> some View {
        HStack(spacing: 3) {
            Text(key)
                .font(.system(size: 10, weight: .semibold, design: .rounded))
                .padding(.horizontal, 4)
                .padding(.vertical, 1)
                .background(RoundedRectangle(cornerRadius: 3).fill(.quaternary))
            Text(label)
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
        }
    }
}

// MARK: - Row

private struct ClipRowView: View {

    let item: ClipItem
    let store: ClipboardStore
    let isSelected: Bool

    /// Purely cosmetic, and deliberately local to the row: hovering shows what a click would
    /// hit without touching the model's selection, so the pointer never causes a scroll.
    @State private var isHovered = false

    private static let relativeFormatter: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f
    }()

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            icon
            VStack(alignment: .leading, spacing: 3) {
                content
                HStack(spacing: 6) {
                    if item.isPinned {
                        Image(systemName: "pin.fill")
                            .font(.system(size: 9))
                            .foregroundStyle(isSelected ? .white : Color.accentColor)
                    }
                    Text(Self.relativeFormatter.localizedString(for: item.timestamp,
                                                                relativeTo: Date()))
                        .font(.system(size: 10))
                        .foregroundStyle(isSelected ? .white.opacity(0.8) : Color.secondary)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(isSelected ? Color.accentColor
                      : (isHovered ? Color.primary.opacity(0.08) : Color.clear))
        )
        .foregroundStyle(isSelected ? Color.white : Color.primary)
        .onHover { isHovered = $0 }
    }

    /// Source app icon when we know it, falling back to a glyph for the payload kind.
    private var icon: some View {
        Group {
            if let appIcon = store.icon(forBundleID: item.sourceAppBundleID) {
                Image(nsImage: appIcon).resizable().frame(width: 18, height: 18)
            } else {
                Image(systemName: item.kind.symbolName)
                    .font(.system(size: 13))
                    .frame(width: 18, height: 18)
            }
        }
        .padding(.top, 1)
    }

    @ViewBuilder
    private var content: some View {
        if item.kind == .image, let image = store.image(for: item) {
            HStack(spacing: 8) {
                Image(nsImage: image)
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(maxWidth: 120, maxHeight: 60)
                    .clipShape(RoundedRectangle(cornerRadius: 4))
                Text(item.previewText)
                    .font(.system(size: 11))
                    .foregroundStyle(isSelected ? .white.opacity(0.9) : Color.secondary)
            }
        } else {
            Text(item.previewText)
                .font(.system(size: 12))
                .lineLimit(3)
                .multilineTextAlignment(.leading)
        }
    }
}
