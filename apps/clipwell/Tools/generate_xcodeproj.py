#!/usr/bin/env python3
"""
Regenerates Clipwell.xcodeproj from whatever Swift files are on disk.

The generated project is *committed to the repo* — you do not need Python or this script to
build Clipwell. It exists only so that adding or moving a source file does not mean
hand-editing a pbxproj: run this, and the project catches up.

Everything is written with deterministic IDs (md5 of the object's path/role), so regenerating
after no change produces a byte-identical file and git stays quiet.

    python3 Tools/generate_xcodeproj.py

Standard library only.
"""

import hashlib
import os
import sys

PROJECT_NAME = "Clipwell"
BUNDLE_ID = "com.thiru.clipwell"
DEPLOYMENT_TARGET = "15.0"
SOURCE_ROOT = "Clipwell"          # directory holding the Swift sources
INFOPLIST = "Support/Info.plist"

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def uid(key):
    """A stable 24-char hex ID, the shape Xcode uses for object references."""
    return hashlib.md5(key.encode("utf-8")).hexdigest()[:24].upper()


def collect_sources():
    """Every .swift under SOURCE_ROOT, grouped by its directory, sorted for stability."""
    tree = {}
    base = os.path.join(ROOT, SOURCE_ROOT)
    for dirpath, dirnames, filenames in os.walk(base):
        dirnames.sort()
        swift = sorted(f for f in filenames if f.endswith(".swift"))
        if not swift:
            continue
        rel = os.path.relpath(dirpath, base)
        tree[rel] = swift
    return tree


def main():
    tree = collect_sources()
    if not tree:
        sys.exit(f"no Swift files found under {SOURCE_ROOT}/")

    build_files = []      # PBXBuildFile entries
    file_refs = []        # PBXFileReference entries
    groups = []           # PBXGroup entries
    source_build_ids = []

    # --- file references + build files -------------------------------------------------
    group_children = {}
    for rel_dir, files in sorted(tree.items()):
        child_ids = []
        for name in files:
            rel_path = name if rel_dir == "." else f"{rel_dir}/{name}"
            key = f"{SOURCE_ROOT}/{rel_path}"
            fref = uid("fileRef:" + key)
            bfile = uid("buildFile:" + key)
            file_refs.append(
                f'\t\t{fref} /* {name} */ = {{isa = PBXFileReference; '
                f'lastKnownFileType = sourcecode.swift; path = {name}; sourceTree = "<group>"; }};'
            )
            build_files.append(
                f'\t\t{bfile} /* {name} in Sources */ = {{isa = PBXBuildFile; '
                f'fileRef = {fref} /* {name} */; }};'
            )
            source_build_ids.append((bfile, name))
            child_ids.append((fref, name))
        group_children[rel_dir] = child_ids

    # --- groups ------------------------------------------------------------------------
    # One group per source subdirectory (App/, Clipboard/, ...), all under a Clipwell group.
    subgroup_ids = []
    for rel_dir in sorted(k for k in group_children if k != "."):
        gid = uid("group:" + rel_dir)
        children = "\n".join(
            f"\t\t\t\t{fid} /* {n} */," for fid, n in group_children[rel_dir]
        )
        groups.append(
            f'\t\t{gid} /* {rel_dir} */ = {{\n'
            f'\t\t\tisa = PBXGroup;\n'
            f'\t\t\tchildren = (\n{children}\n\t\t\t);\n'
            f'\t\t\tpath = {rel_dir};\n'
            f'\t\t\tsourceTree = "<group>";\n'
            f'\t\t}};'
        )
        subgroup_ids.append((gid, rel_dir))

    # Loose files sitting directly in SOURCE_ROOT.
    root_loose = "\n".join(
        f"\t\t\t\t{fid} /* {n} */," for fid, n in group_children.get(".", [])
    )

    source_group_id = uid("group:sourceRoot")
    source_children = "\n".join(
        f"\t\t\t\t{gid} /* {name} */," for gid, name in subgroup_ids
    )
    if root_loose:
        source_children = source_children + "\n" + root_loose
    groups.append(
        f'\t\t{source_group_id} /* {SOURCE_ROOT} */ = {{\n'
        f'\t\t\tisa = PBXGroup;\n'
        f'\t\t\tchildren = (\n{source_children}\n\t\t\t);\n'
        f'\t\t\tpath = {SOURCE_ROOT};\n'
        f'\t\t\tsourceTree = "<group>";\n'
        f'\t\t}};'
    )

    # Info.plist lives outside the source tree so it is never swept into a build phase.
    plist_ref = uid("fileRef:infoplist")
    file_refs.append(
        f'\t\t{plist_ref} /* Info.plist */ = {{isa = PBXFileReference; '
        f'lastKnownFileType = text.plist.xml; path = Info.plist; sourceTree = "<group>"; }};'
    )
    # App icon: a file reference AND a build file, because unlike Info.plist it has to be
    # copied into Contents/Resources rather than processed.
    icon_ref = uid("fileRef:appicon")
    icon_build = uid("buildFile:appicon")
    file_refs.append(
        f'\t\t{icon_ref} /* AppIcon.icns */ = {{isa = PBXFileReference; '
        f'lastKnownFileType = image.icns; path = AppIcon.icns; sourceTree = "<group>"; }};'
    )
    build_files.append(
        f'\t\t{icon_build} /* AppIcon.icns in Resources */ = {{isa = PBXBuildFile; '
        f'fileRef = {icon_ref} /* AppIcon.icns */; }};'
    )
    support_group_id = uid("group:support")
    groups.append(
        f'\t\t{support_group_id} /* Support */ = {{\n'
        f'\t\t\tisa = PBXGroup;\n'
        f'\t\t\tchildren = (\n'
        f'\t\t\t\t{plist_ref} /* Info.plist */,\n'
        f'\t\t\t\t{icon_ref} /* AppIcon.icns */,\n'
        f'\t\t\t);\n'
        f'\t\t\tpath = Support;\n'
        f'\t\t\tsourceTree = "<group>";\n'
        f'\t\t}};'
    )

    product_ref = uid("fileRef:product")
    file_refs.append(
        f'\t\t{product_ref} /* {PROJECT_NAME}.app */ = {{isa = PBXFileReference; '
        f'explicitFileType = wrapper.application; includeInIndex = 0; '
        f'path = {PROJECT_NAME}.app; sourceTree = BUILT_PRODUCTS_DIR; }};'
    )
    products_group_id = uid("group:products")
    groups.append(
        f'\t\t{products_group_id} /* Products */ = {{\n'
        f'\t\t\tisa = PBXGroup;\n'
        f'\t\t\tchildren = (\n\t\t\t\t{product_ref} /* {PROJECT_NAME}.app */,\n\t\t\t);\n'
        f'\t\t\tname = Products;\n'
        f'\t\t\tsourceTree = "<group>";\n'
        f'\t\t}};'
    )

    main_group_id = uid("group:main")
    groups.append(
        f'\t\t{main_group_id} = {{\n'
        f'\t\t\tisa = PBXGroup;\n'
        f'\t\t\tchildren = (\n'
        f'\t\t\t\t{source_group_id} /* {SOURCE_ROOT} */,\n'
        f'\t\t\t\t{support_group_id} /* Support */,\n'
        f'\t\t\t\t{products_group_id} /* Products */,\n'
        f'\t\t\t);\n'
        f'\t\t\tsourceTree = "<group>";\n'
        f'\t\t}};'
    )

    # --- ids ----------------------------------------------------------------------------
    project_id = uid("project")
    target_id = uid("target")
    sources_phase = uid("phase:sources")
    frameworks_phase = uid("phase:frameworks")
    resources_phase = uid("phase:resources")
    proj_cfg_list = uid("cfglist:project")
    targ_cfg_list = uid("cfglist:target")
    proj_debug = uid("cfg:project:debug")
    proj_release = uid("cfg:project:release")
    targ_debug = uid("cfg:target:debug")
    targ_release = uid("cfg:target:release")

    sources_list = "\n".join(
        f"\t\t\t\t{bid} /* {name} in Sources */," for bid, name in source_build_ids
    )

    shared_project_settings = f"""\t\t\t\tALWAYS_SEARCH_USER_PATHS = NO;
\t\t\t\tCLANG_ENABLE_MODULES = YES;
\t\t\t\tCLANG_ENABLE_OBJC_ARC = YES;
\t\t\t\tCLANG_WARN_BLOCK_CAPTURE_AUTORELEASING = YES;
\t\t\t\tCLANG_WARN_BOOL_CONVERSION = YES;
\t\t\t\tCLANG_WARN_DOCUMENTATION_COMMENTS = YES;
\t\t\t\tCLANG_WARN_UNREACHABLE_CODE = YES;
\t\t\t\tCOPY_PHASE_STRIP = NO;
\t\t\t\tENABLE_STRICT_OBJC_MSGSEND = YES;
\t\t\t\tGCC_NO_COMMON_BLOCKS = YES;
\t\t\t\tGCC_WARN_UNDECLARED_SELECTOR = YES;
\t\t\t\tGCC_WARN_UNINITIALIZED_AUTOS = YES;
\t\t\t\tGCC_WARN_UNUSED_FUNCTION = YES;
\t\t\t\tGCC_WARN_UNUSED_VARIABLE = YES;
\t\t\t\tMACOSX_DEPLOYMENT_TARGET = {DEPLOYMENT_TARGET};
\t\t\t\tSDKROOT = macosx;
\t\t\t\tSWIFT_VERSION = 5.0;"""

    # Sandbox OFF is not optional: the Accessibility API is unavailable to sandboxed apps.
    shared_target_settings = f"""\t\t\t\tCODE_SIGN_IDENTITY = "-";
\t\t\t\tCODE_SIGN_INJECT_BASE_ENTITLEMENTS = NO;
\t\t\t\tCODE_SIGN_STYLE = Manual;
\t\t\t\tCOMBINE_HIDPI_IMAGES = YES;
\t\t\t\tCURRENT_PROJECT_VERSION = 1;
\t\t\t\tDEVELOPMENT_TEAM = "";
\t\t\t\tENABLE_APP_SANDBOX = NO;
\t\t\t\tENABLE_HARDENED_RUNTIME = NO;
\t\t\t\tENABLE_USER_SCRIPT_SANDBOXING = NO;
\t\t\t\tGENERATE_INFOPLIST_FILE = NO;
\t\t\t\tINFOPLIST_FILE = {INFOPLIST};
\t\t\t\tLD_RUNPATH_SEARCH_PATHS = (
\t\t\t\t\t"$(inherited)",
\t\t\t\t\t"@executable_path/../Frameworks",
\t\t\t\t);
\t\t\t\tMARKETING_VERSION = 1.0;
\t\t\t\tPRODUCT_BUNDLE_IDENTIFIER = {BUNDLE_ID};
\t\t\t\tPRODUCT_NAME = "$(TARGET_NAME)";
\t\t\t\tPROVISIONING_PROFILE_SPECIFIER = "";
\t\t\t\tSWIFT_EMIT_LOC_STRINGS = NO;"""

    pbxproj = f"""// !$*UTF8*$!
{{
\tarchiveVersion = 1;
\tclasses = {{
\t}};
\tobjectVersion = 56;
\tobjects = {{

/* Begin PBXBuildFile section */
{chr(10).join(sorted(build_files))}
/* End PBXBuildFile section */

/* Begin PBXFileReference section */
{chr(10).join(sorted(file_refs))}
/* End PBXFileReference section */

/* Begin PBXFrameworksBuildPhase section */
\t\t{frameworks_phase} /* Frameworks */ = {{
\t\t\tisa = PBXFrameworksBuildPhase;
\t\t\tbuildActionMask = 2147483647;
\t\t\tfiles = (
\t\t\t);
\t\t\trunOnlyForDeploymentPostprocessing = 0;
\t\t}};
/* End PBXFrameworksBuildPhase section */

/* Begin PBXGroup section */
{chr(10).join(groups)}
/* End PBXGroup section */

/* Begin PBXNativeTarget section */
\t\t{target_id} /* {PROJECT_NAME} */ = {{
\t\t\tisa = PBXNativeTarget;
\t\t\tbuildConfigurationList = {targ_cfg_list} /* Build configuration list for PBXNativeTarget "{PROJECT_NAME}" */;
\t\t\tbuildPhases = (
\t\t\t\t{sources_phase} /* Sources */,
\t\t\t\t{frameworks_phase} /* Frameworks */,
\t\t\t\t{resources_phase} /* Resources */,
\t\t\t);
\t\t\tbuildRules = (
\t\t\t);
\t\t\tdependencies = (
\t\t\t);
\t\t\tname = {PROJECT_NAME};
\t\t\tproductName = {PROJECT_NAME};
\t\t\tproductReference = {product_ref} /* {PROJECT_NAME}.app */;
\t\t\tproductType = "com.apple.product-type.application";
\t\t}};
/* End PBXNativeTarget section */

/* Begin PBXProject section */
\t\t{project_id} /* Project object */ = {{
\t\t\tisa = PBXProject;
\t\t\tattributes = {{
\t\t\t\tBuildIndependentTargetsInParallel = 1;
\t\t\t\tLastSwiftUpdateCheck = 1600;
\t\t\t\tLastUpgradeCheck = 1600;
\t\t\t\tTargetAttributes = {{
\t\t\t\t\t{target_id} = {{
\t\t\t\t\t\tCreatedOnToolsVersion = 16.0;
\t\t\t\t\t}};
\t\t\t\t}};
\t\t\t}};
\t\t\tbuildConfigurationList = {proj_cfg_list} /* Build configuration list for PBXProject "{PROJECT_NAME}" */;
\t\t\tcompatibilityVersion = "Xcode 14.0";
\t\t\tdevelopmentRegion = en;
\t\t\thasScannedForEncodings = 0;
\t\t\tknownRegions = (
\t\t\t\ten,
\t\t\t\tBase,
\t\t\t);
\t\t\tmainGroup = {main_group_id};
\t\t\tproductRefGroup = {products_group_id} /* Products */;
\t\t\tprojectDirPath = "";
\t\t\tprojectRoot = "";
\t\t\ttargets = (
\t\t\t\t{target_id} /* {PROJECT_NAME} */,
\t\t\t);
\t\t}};
/* End PBXProject section */

/* Begin PBXResourcesBuildPhase section */
\t\t{resources_phase} /* Resources */ = {{
\t\t\tisa = PBXResourcesBuildPhase;
\t\t\tbuildActionMask = 2147483647;
\t\t\tfiles = (
\t\t\t\t{icon_build} /* AppIcon.icns in Resources */,
\t\t\t);
\t\t\trunOnlyForDeploymentPostprocessing = 0;
\t\t}};
/* End PBXResourcesBuildPhase section */

/* Begin PBXSourcesBuildPhase section */
\t\t{sources_phase} /* Sources */ = {{
\t\t\tisa = PBXSourcesBuildPhase;
\t\t\tbuildActionMask = 2147483647;
\t\t\tfiles = (
{sources_list}
\t\t\t);
\t\t\trunOnlyForDeploymentPostprocessing = 0;
\t\t}};
/* End PBXSourcesBuildPhase section */

/* Begin XCBuildConfiguration section */
\t\t{proj_debug} /* Debug */ = {{
\t\t\tisa = XCBuildConfiguration;
\t\t\tbuildSettings = {{
{shared_project_settings}
\t\t\t\tDEBUG_INFORMATION_FORMAT = dwarf;
\t\t\t\tENABLE_TESTABILITY = YES;
\t\t\t\tGCC_OPTIMIZATION_LEVEL = 0;
\t\t\t\tONLY_ACTIVE_ARCH = YES;
\t\t\t\tSWIFT_ACTIVE_COMPILATION_CONDITIONS = DEBUG;
\t\t\t\tSWIFT_OPTIMIZATION_LEVEL = "-Onone";
\t\t\t}};
\t\t\tname = Debug;
\t\t}};
\t\t{proj_release} /* Release */ = {{
\t\t\tisa = XCBuildConfiguration;
\t\t\tbuildSettings = {{
{shared_project_settings}
\t\t\t\tDEBUG_INFORMATION_FORMAT = "dwarf-with-dsym";
\t\t\t\tENABLE_NS_ASSERTIONS = NO;
\t\t\t\tSWIFT_COMPILATION_MODE = wholemodule;
\t\t\t\tSWIFT_OPTIMIZATION_LEVEL = "-O";
\t\t\t}};
\t\t\tname = Release;
\t\t}};
\t\t{targ_debug} /* Debug */ = {{
\t\t\tisa = XCBuildConfiguration;
\t\t\tbuildSettings = {{
{shared_target_settings}
\t\t\t}};
\t\t\tname = Debug;
\t\t}};
\t\t{targ_release} /* Release */ = {{
\t\t\tisa = XCBuildConfiguration;
\t\t\tbuildSettings = {{
{shared_target_settings}
\t\t\t}};
\t\t\tname = Release;
\t\t}};
/* End XCBuildConfiguration section */

/* Begin XCConfigurationList section */
\t\t{proj_cfg_list} /* Build configuration list for PBXProject "{PROJECT_NAME}" */ = {{
\t\t\tisa = XCConfigurationList;
\t\t\tbuildConfigurations = (
\t\t\t\t{proj_debug} /* Debug */,
\t\t\t\t{proj_release} /* Release */,
\t\t\t);
\t\t\tdefaultConfigurationIsVisible = 0;
\t\t\tdefaultConfigurationName = Release;
\t\t}};
\t\t{targ_cfg_list} /* Build configuration list for PBXNativeTarget "{PROJECT_NAME}" */ = {{
\t\t\tisa = XCConfigurationList;
\t\t\tbuildConfigurations = (
\t\t\t\t{targ_debug} /* Debug */,
\t\t\t\t{targ_release} /* Release */,
\t\t\t);
\t\t\tdefaultConfigurationIsVisible = 0;
\t\t\tdefaultConfigurationName = Release;
\t\t}};
/* End XCConfigurationList section */
\t}};
\trootObject = {project_id} /* Project object */;
}}
"""

    scheme = f"""<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion = "1600" version = "1.7">
   <BuildAction parallelizeBuildables = "YES" buildImplicitDependencies = "YES">
      <BuildActionEntries>
         <BuildActionEntry buildForTesting = "YES" buildForRunning = "YES" buildForProfiling = "YES" buildForArchiving = "YES" buildForAnalyzing = "YES">
            <BuildableReference
               BuildableIdentifier = "primary"
               BlueprintIdentifier = "{target_id}"
               BuildableName = "{PROJECT_NAME}.app"
               BlueprintName = "{PROJECT_NAME}"
               ReferencedContainer = "container:{PROJECT_NAME}.xcodeproj">
            </BuildableReference>
         </BuildActionEntry>
      </BuildActionEntries>
   </BuildAction>
   <TestAction buildConfiguration = "Debug" selectedDebuggerIdentifier = "Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier = "Xcode.DebuggerFoundation.Launcher.LLDB" shouldUseLaunchSchemeArgsEnv = "YES">
      <Testables>
      </Testables>
   </TestAction>
   <LaunchAction buildConfiguration = "Debug" selectedDebuggerIdentifier = "Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier = "Xcode.DebuggerFoundation.Launcher.LLDB" launchStyle = "0" useCustomWorkingDirectory = "NO" ignoresPersistentStateOnLaunch = "NO" debugDocumentVersioning = "YES" debugServiceExtension = "internal" allowLocationSimulation = "YES">
      <BuildableProductRunnable runnableDebuggingMode = "0">
         <BuildableReference
            BuildableIdentifier = "primary"
            BlueprintIdentifier = "{target_id}"
            BuildableName = "{PROJECT_NAME}.app"
            BlueprintName = "{PROJECT_NAME}"
            ReferencedContainer = "container:{PROJECT_NAME}.xcodeproj">
         </BuildableReference>
      </BuildableProductRunnable>
   </LaunchAction>
   <ProfileAction buildConfiguration = "Release" shouldUseLaunchSchemeArgsEnv = "YES" savedToolIdentifier = "" useCustomWorkingDirectory = "NO" debugDocumentVersioning = "YES">
      <BuildableProductRunnable runnableDebuggingMode = "0">
         <BuildableReference
            BuildableIdentifier = "primary"
            BlueprintIdentifier = "{target_id}"
            BuildableName = "{PROJECT_NAME}.app"
            BlueprintName = "{PROJECT_NAME}"
            ReferencedContainer = "container:{PROJECT_NAME}.xcodeproj">
         </BuildableReference>
      </BuildableProductRunnable>
   </ProfileAction>
   <AnalyzeAction buildConfiguration = "Debug">
   </AnalyzeAction>
   <ArchiveAction buildConfiguration = "Release" revealArchiveInOrganizer = "YES">
   </ArchiveAction>
</Scheme>
"""

    proj_dir = os.path.join(ROOT, f"{PROJECT_NAME}.xcodeproj")
    scheme_dir = os.path.join(proj_dir, "xcshareddata", "xcschemes")
    os.makedirs(scheme_dir, exist_ok=True)

    with open(os.path.join(proj_dir, "project.pbxproj"), "w") as f:
        f.write(pbxproj)
    with open(os.path.join(scheme_dir, f"{PROJECT_NAME}.xcscheme"), "w") as f:
        f.write(scheme)

    total = sum(len(v) for v in tree.values())
    print(f"wrote {PROJECT_NAME}.xcodeproj with {total} Swift files "
          f"across {len(tree)} groups")


if __name__ == "__main__":
    main()
